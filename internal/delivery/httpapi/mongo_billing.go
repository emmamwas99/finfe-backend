package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"finfe/internal/infrastructure/paystack"
)

var errInvalidPayment = errors.New("verified payment did not match expected amount or currency")

func (a *MongoAPI) subscription(ctx context.Context, uid uuid.UUID) (map[string]any, error) {
	var doc bson.M
	err := a.database.DB.Collection("subscriptions").FindOne(ctx, bson.M{"_id": uid.String()}).Decode(&doc)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": doc["status"], "trialEndsAt": doc["trial_ends_at"], "currentPeriodEnd": doc["current_period_end"], "billing": doc["billing"], "period": doc["period"], "hasManageLink": stringField(doc, "paystack_subscription_code") != ""}, nil
}

func (a *MongoAPI) billingSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := a.subscription(r.Context(), principal(r).UserID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		respond(w, 200, map[string]any{"subscription": nil})
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"subscription": sub})
}

func (a *MongoAPI) billingPayments(w http.ResponseWriter, r *http.Request) {
	rows, err := a.docs(r.Context(), "payments", bson.M{"user_id": principal(r).UserID.String()})
	if err != nil {
		a.serverError(w, err)
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		ti, _ := rows[i]["created_at"].(time.Time)
		tj, _ := rows[j]["created_at"].(time.Time)
		return ti.After(tj)
	})
	if len(rows) > 50 {
		rows = rows[:50]
	}
	data := make([]bson.M, 0, len(rows))
	for _, p := range rows {
		data = append(data, bson.M{"reference": p["reference"], "amount_minor": p["amount_minor"], "period": p["period"], "method": p["method"], "status": p["status"], "paid_at": p["paid_at"], "created_at": p["created_at"]})
	}
	respond(w, 200, map[string]any{"payments": data})
}

func (a *MongoAPI) billingCheckout(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "checkout") {
		return
	}
	var v struct{ Period, Method string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	period := paystack.Period(v.Period)
	amount, ok := paystack.PriceMinor[period]
	if !ok || (v.Method != "card" && v.Method != "mpesa") {
		fail(w, 400, "validation_failed", "Choose a valid plan and payment method")
		return
	}
	if !a.cfg.BillingDemo && a.cfg.PaystackSecretKey == "" {
		fail(w, 503, "billing_unavailable", "Payments are not configured")
		return
	}
	ref := "finfe_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := a.database.DB.Collection("payments").InsertOne(r.Context(), bson.M{"_id": ref, "reference": ref, "user_id": principal(r).UserID.String(), "amount_minor": amount, "currency": "KES", "period": v.Period, "method": v.Method, "channel": nil, "status": "pending", "paid_at": nil, "created_at": time.Now().UTC()})
	if err != nil {
		a.serverError(w, err)
		return
	}
	if a.cfg.BillingDemo {
		respond(w, 200, map[string]string{"reference": ref, "url": "/demo-checkout?ref=" + ref})
		return
	}
	u, err := a.authStore.GetUserByID(r.Context(), principal(r).UserID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	channels := []string{"mobile_money"}
	if v.Method == "card" {
		channels = []string{"card"}
	}
	plan := ""
	if v.Method == "card" {
		if period == paystack.Monthly {
			plan = a.cfg.PaystackPlanMonthly
		} else {
			plan = a.cfg.PaystackPlanYearly
		}
	}
	meta, _ := json.Marshal(map[string]string{"period": v.Period, "user_id": u.ID.String()})
	url, err := paystack.NewClient(a.cfg.PaystackSecretKey).Initialize(r.Context(), paystack.InitializeInput{Email: u.Email, AmountMinor: amount, Currency: "KES", Reference: ref, Channels: channels, Plan: plan, CallbackURL: a.cfg.AppURL + "/app/billing", Metadata: string(meta)})
	if err != nil {
		a.log.Error("checkout init failed", "error", err)
		fail(w, 503, "billing_unavailable", "Could not start checkout")
		return
	}
	respond(w, 200, map[string]string{"reference": ref, "url": url})
}

func (a *MongoAPI) payment(ctx context.Context, ref string, uid *uuid.UUID) (bson.M, error) {
	filter := bson.M{"_id": ref}
	if uid != nil {
		filter["user_id"] = uid.String()
	}
	var doc bson.M
	err := a.database.DB.Collection("payments").FindOne(ctx, filter).Decode(&doc)
	return doc, err
}

func (a *MongoAPI) completePayment(ctx context.Context, ref string, data paystack.EventData) (string, error) {
	status := ""
	err := a.database.InTx(ctx, func(tx context.Context) error {
		p, err := a.payment(tx, ref, nil)
		if err != nil {
			return err
		}
		status = stringField(p, "status")
		if status == "success" {
			return nil
		}
		if data.Reference != ref || data.Status != "success" || data.Currency != stringField(p, "currency") || data.Amount != int64Field(p, "amount_minor") {
			return errInvalidPayment
		}
		uid := stringField(p, "user_id")
		var sub bson.M
		if err := a.database.DB.Collection("subscriptions").FindOne(tx, bson.M{"_id": uid}).Decode(&sub); err != nil {
			return err
		}
		var trial, end *time.Time
		if t, ok := sub["trial_ends_at"].(time.Time); ok {
			trial = &t
		}
		if t, ok := sub["current_period_end"].(time.Time); ok {
			end = &t
		}
		period := stringField(p, "period")
		next := paystack.Extend(time.Now().UTC(), trial, end, paystack.Period(period))
		billing := "prepaid"
		if stringField(p, "method") == "card" && (period == "monthly" && a.cfg.PaystackPlanMonthly != "" || period == "yearly" && a.cfg.PaystackPlanYearly != "") {
			billing = "card"
		}
		_, err = a.database.DB.Collection("payments").UpdateOne(tx, bson.M{"_id": ref}, bson.M{"$set": bson.M{"status": "success", "channel": data.Channel, "paid_at": time.Now().UTC()}})
		if err != nil {
			return err
		}
		set := bson.M{"status": "active", "current_period_end": next, "billing": billing, "period": period, "updated_at": time.Now().UTC()}
		if data.Customer.CustomerCode != "" {
			set["paystack_customer_code"] = data.Customer.CustomerCode
		}
		if _, err := a.database.DB.Collection("subscriptions").UpdateOne(tx, bson.M{"_id": uid}, bson.M{"$set": set}); err != nil {
			return err
		}
		if _, err := a.database.DB.Collection("audit_events").InsertOne(tx, bson.M{"user_id": uid, "action": "billing.payment_success", "metadata": bson.M{"reference": ref}, "ip": "", "user_agent": "", "created_at": time.Now().UTC()}); err != nil {
			return err
		}
		status = "success"
		return nil
	})
	return status, err
}

func (a *MongoAPI) billingVerify(w http.ResponseWriter, r *http.Request) {
	var v struct{ Reference string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	uid := principal(r).UserID
	p, err := a.payment(r.Context(), v.Reference, &uid)
	if errors.Is(err, mongo.ErrNoDocuments) {
		fail(w, 404, "not_found", "Payment not found")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	status := stringField(p, "status")
	if status == "pending" && !a.cfg.BillingDemo {
		data, err := paystack.NewClient(a.cfg.PaystackSecretKey).Verify(r.Context(), v.Reference)
		if err != nil {
			fail(w, 503, "billing_unavailable", "Could not verify payment")
			return
		}
		if data.Status == "success" {
			status, err = a.completePayment(r.Context(), v.Reference, data)
			if err != nil {
				a.log.Error("payment mismatch", "error", err)
				fail(w, 409, "payment_mismatch", "Payment could not be matched")
				return
			}
		}
		if data.Status == "failed" || data.Status == "abandoned" {
			status = "failed"
			_, _ = a.database.DB.Collection("payments").UpdateOne(r.Context(), bson.M{"_id": v.Reference, "user_id": principal(r).UserID.String(), "status": "pending"}, bson.M{"$set": bson.M{"status": "failed"}})
		}
	}
	sub, _ := a.subscription(r.Context(), principal(r).UserID)
	respond(w, 200, map[string]any{"status": status, "subscription": sub})
}

func (a *MongoAPI) billingManage(w http.ResponseWriter, r *http.Request) {
	var sub bson.M
	err := a.database.DB.Collection("subscriptions").FindOne(r.Context(), bson.M{"_id": principal(r).UserID.String(), "paystack_subscription_code": bson.M{"$ne": nil}}).Decode(&sub)
	if errors.Is(err, mongo.ErrNoDocuments) || stringField(sub, "paystack_subscription_code") == "" {
		fail(w, 404, "not_found", "No card subscription to manage")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	url, err := paystack.NewClient(a.cfg.PaystackSecretKey).ManageLink(r.Context(), stringField(sub, "paystack_subscription_code"))
	if err != nil {
		fail(w, 503, "billing_unavailable", "Could not open subscription management")
		return
	}
	respond(w, 200, map[string]string{"url": url})
}

func (a *MongoAPI) billingDemoInfo(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.BillingDemo {
		fail(w, 404, "not_found", "Demo checkout unavailable")
		return
	}
	uid := principal(r).UserID
	p, err := a.payment(r.Context(), chi.URLParam(r, "reference"), &uid)
	if errors.Is(err, mongo.ErrNoDocuments) {
		fail(w, 404, "not_found", "Checkout not found")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"checkout": bson.M{"reference": p["reference"], "period": p["period"], "method": p["method"], "amount_minor": p["amount_minor"], "status": p["status"]}})
}

func (a *MongoAPI) billingDemoComplete(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.BillingDemo {
		fail(w, 404, "not_found", "Demo checkout unavailable")
		return
	}
	var v struct{ Paid bool }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	ref := chi.URLParam(r, "reference")
	uid := principal(r).UserID
	p, err := a.payment(r.Context(), ref, &uid)
	if errors.Is(err, mongo.ErrNoDocuments) {
		fail(w, 404, "not_found", "Checkout not found")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	status := stringField(p, "status")
	if status != "pending" {
		respond(w, 200, map[string]string{"status": status})
		return
	}
	if !v.Paid {
		if _, err := a.database.DB.Collection("payments").UpdateOne(r.Context(), bson.M{"_id": ref, "status": "pending"}, bson.M{"$set": bson.M{"status": "failed"}}); err != nil {
			a.serverError(w, err)
			return
		}
		respond(w, 200, map[string]string{"status": "failed"})
		return
	}
	status, err = a.completePayment(r.Context(), ref, paystack.EventData{Status: "success", Reference: ref, Amount: int64Field(p, "amount_minor"), Currency: "KES", Channel: "demo"})
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"status": status})
}

func (a *MongoAPI) billingWebhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		badJSON(w, err)
		return
	}
	if !paystack.VerifySignature(raw, r.Header, paystack.SignatureConfig{WebhookSecret: a.cfg.PaystackWebhookSecret, SecretKey: a.cfg.PaystackSecretKey}, time.Now()) {
		fail(w, 401, "bad_signature", "Invalid webhook signature")
		return
	}
	var event paystack.Event
	if err := json.Unmarshal(raw, &event); err != nil {
		badJSON(w, err)
		return
	}
	action := paystack.Map(event, time.Now().UTC())
	switch action.Type {
	case "payment":
		if event.Data.Status == "" {
			event.Data.Status = "success"
		}
		_, err = a.completePayment(r.Context(), action.Reference, event.Data)
		if errors.Is(err, mongo.ErrNoDocuments) && action.CustomerCode != "" && paystack.AmountMatches(action.AmountMinor, action.Period) {
			var sub bson.M
			findErr := a.database.DB.Collection("subscriptions").FindOne(r.Context(), bson.M{"paystack_customer_code": action.CustomerCode}).Decode(&sub)
			if findErr == nil {
				_, err = a.database.DB.Collection("payments").InsertOne(r.Context(), bson.M{"_id": action.Reference, "reference": action.Reference, "user_id": sub["user_id"], "amount_minor": action.AmountMinor, "currency": "KES", "period": action.Period, "method": "card", "status": "pending", "created_at": time.Now().UTC()})
				if mongo.IsDuplicateKeyError(err) {
					err = nil
				}
				if err == nil {
					_, err = a.completePayment(r.Context(), action.Reference, event.Data)
				}
			}
		}
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) && !errors.Is(err, errInvalidPayment) {
			a.serverError(w, err)
			return
		}
	case "subscription":
		if action.CustomerCode != "" {
			set := bson.M{"status": action.Status, "updated_at": time.Now().UTC()}
			if action.SubscriptionCode != "" {
				set["paystack_subscription_code"] = action.SubscriptionCode
			}
			if action.EmailToken != "" {
				set["paystack_email_token"] = action.EmailToken
			}
			if action.NextPaymentDate != nil {
				set["current_period_end"] = action.NextPaymentDate
			}
			if _, err := a.database.DB.Collection("subscriptions").UpdateOne(r.Context(), bson.M{"paystack_customer_code": action.CustomerCode}, bson.M{"$set": set}); err != nil {
				a.serverError(w, err)
				return
			}
		}
	case "payment_failed":
		if action.CustomerCode != "" {
			if _, err := a.database.DB.Collection("subscriptions").UpdateOne(r.Context(), bson.M{"paystack_customer_code": action.CustomerCode}, bson.M{"$set": bson.M{"status": "past_due", "updated_at": time.Now().UTC()}}); err != nil {
				a.serverError(w, err)
				return
			}
		}
	}
	_, _ = a.database.DB.Collection("webhook_events").UpdateOne(r.Context(), bson.M{"_id": paystack.Key(event)}, bson.M{"$setOnInsert": bson.M{"received_at": time.Now().UTC()}}, options.UpdateOne().SetUpsert(true))
	w.WriteHeader(200)
}

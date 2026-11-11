package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"finfe/internal/infrastructure/paystack"
)

func (a *API) subscription(ctx context.Context, uid uuid.UUID) (map[string]any, error) {
	var status string
	var trial, end *time.Time
	var billing, period, code *string
	err := a.db.QueryRow(ctx, `SELECT status,trial_ends_at,current_period_end,billing,period,paystack_subscription_code FROM subscriptions WHERE user_id=$1`, uid).Scan(&status, &trial, &end, &billing, &period, &code)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": status, "trialEndsAt": trial, "currentPeriodEnd": end, "billing": billing, "period": period, "hasManageLink": code != nil && *code != ""}, nil
}
func (a *API) billingSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := a.subscription(r.Context(), principal(r).UserID)
	if err == pgx.ErrNoRows {
		respond(w, 200, map[string]any{"subscription": nil})
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"subscription": sub})
}
func (a *API) billingPayments(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT reference,amount_minor,period,method,status,paid_at,created_at FROM payments WHERE user_id=$1 ORDER BY created_at DESC LIMIT 50`, principal(r).UserID)
	if err != nil {
		serverError(a, w, err)
		return
	}
	data, err := collect(rows)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"payments": data})
}
func (a *API) billingCheckout(w http.ResponseWriter, r *http.Request) {
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
	_, err := a.db.Exec(r.Context(), `INSERT INTO payments(user_id,reference,amount_minor,currency,period,method,status) VALUES($1,$2,$3,'KES',$4,$5,'pending')`, principal(r).UserID, ref, amount, v.Period, v.Method)
	if err != nil {
		serverError(a, w, err)
		return
	}
	if a.cfg.BillingDemo {
		respond(w, 200, map[string]string{"reference": ref, "url": "/demo-checkout?ref=" + ref})
		return
	}
	u, err := a.q.GetUserByID(r.Context(), principal(r).UserID)
	if err != nil {
		serverError(a, w, err)
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
func (a *API) completePayment(ctx context.Context, ref string, data paystack.EventData) (string, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var uid uuid.UUID
	var amount int64
	var currency, period, method, status string
	err = tx.QueryRow(ctx, `SELECT user_id,amount_minor,currency,period,method,status FROM payments WHERE reference=$1 FOR UPDATE`, ref).Scan(&uid, &amount, &currency, &period, &method, &status)
	if err != nil {
		return "", err
	}
	if status == "success" {
		return status, nil
	}
	if data.Reference != ref || data.Status != "success" || data.Currency != currency || data.Amount != amount {
		return "", errInvalidPayment
	}
	var trial, end *time.Time
	err = tx.QueryRow(ctx, `SELECT trial_ends_at,current_period_end FROM subscriptions WHERE user_id=$1 FOR UPDATE`, uid).Scan(&trial, &end)
	if err != nil {
		return "", err
	}
	next := paystack.Extend(time.Now().UTC(), trial, end, paystack.Period(period))
	billing := "prepaid"
	if method == "card" && (period == "monthly" && a.cfg.PaystackPlanMonthly != "" || period == "yearly" && a.cfg.PaystackPlanYearly != "") {
		billing = "card"
	}
	if _, err = tx.Exec(ctx, `UPDATE payments SET status='success',channel=$2,paid_at=$3 WHERE reference=$1`, ref, data.Channel, time.Now().UTC()); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE subscriptions SET status='active',current_period_end=$2,billing=$3,period=$4,paystack_customer_code=coalesce(nullif($5,''),paystack_customer_code),updated_at=now() WHERE user_id=$1`, uid, next, billing, period, data.Customer.CustomerCode); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(user_id,action,metadata) VALUES($1,'billing.payment_success',jsonb_build_object('reference',$2))`, uid, ref); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return "success", nil
}

var errInvalidPayment = errors.New("verified payment did not match expected amount or currency")

func (a *API) billingVerify(w http.ResponseWriter, r *http.Request) {
	var v struct{ Reference string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	var status string
	err := a.db.QueryRow(r.Context(), `SELECT status FROM payments WHERE reference=$1 AND user_id=$2`, v.Reference, principal(r).UserID).Scan(&status)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Payment not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
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
			_, _ = a.db.Exec(r.Context(), `UPDATE payments SET status='failed' WHERE reference=$1 AND user_id=$2 AND status='pending'`, v.Reference, principal(r).UserID)
		}
	}
	sub, _ := a.subscription(r.Context(), principal(r).UserID)
	respond(w, 200, map[string]any{"status": status, "subscription": sub})
}
func (a *API) billingManage(w http.ResponseWriter, r *http.Request) {
	var code string
	err := a.db.QueryRow(r.Context(), `SELECT paystack_subscription_code FROM subscriptions WHERE user_id=$1 AND paystack_subscription_code IS NOT NULL`, principal(r).UserID).Scan(&code)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "No card subscription to manage")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	url, err := paystack.NewClient(a.cfg.PaystackSecretKey).ManageLink(r.Context(), code)
	if err != nil {
		fail(w, 503, "billing_unavailable", "Could not open subscription management")
		return
	}
	respond(w, 200, map[string]string{"url": url})
}
func (a *API) billingDemoInfo(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.BillingDemo {
		fail(w, 404, "not_found", "Demo checkout unavailable")
		return
	}
	ref := chi.URLParam(r, "reference")
	row, err := one(r.Context(), a.db, `SELECT reference,period,method,amount_minor,status FROM payments WHERE reference=$1 AND user_id=$2`, ref, principal(r).UserID)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Checkout not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"checkout": row})
}
func (a *API) billingDemoComplete(w http.ResponseWriter, r *http.Request) {
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
	var amount int64
	var status string
	err := a.db.QueryRow(r.Context(), `SELECT amount_minor,status FROM payments WHERE reference=$1 AND user_id=$2`, ref, principal(r).UserID).Scan(&amount, &status)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Checkout not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	if status != "pending" {
		respond(w, 200, map[string]string{"status": status})
		return
	}
	if !v.Paid {
		_, err = a.db.Exec(r.Context(), `UPDATE payments SET status='failed' WHERE reference=$1 AND status='pending'`, ref)
		if err != nil {
			serverError(a, w, err)
			return
		}
		respond(w, 200, map[string]string{"status": "failed"})
		return
	}
	data := paystack.EventData{Status: "success", Reference: ref, Amount: amount, Currency: "KES", Channel: "demo"}
	status, err = a.completePayment(r.Context(), ref, data)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]string{"status": status})
}
func (a *API) billingWebhook(w http.ResponseWriter, r *http.Request) {
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
		if err == pgx.ErrNoRows && action.CustomerCode != "" && paystack.AmountMatches(action.AmountMinor, action.Period) {
			var uid uuid.UUID
			findErr := a.db.QueryRow(r.Context(), `SELECT user_id FROM subscriptions WHERE paystack_customer_code=$1`, action.CustomerCode).Scan(&uid)
			if findErr == nil {
				_, err = a.db.Exec(r.Context(), `INSERT INTO payments(user_id,reference,amount_minor,currency,period,method,status) VALUES($1,$2,$3,'KES',$4,'card','pending') ON CONFLICT (reference) DO NOTHING`, uid, action.Reference, action.AmountMinor, action.Period)
				if err == nil {
					_, err = a.completePayment(r.Context(), action.Reference, event.Data)
				}
			}
		}
		if err != nil && err != pgx.ErrNoRows && err != errInvalidPayment {
			serverError(a, w, err)
			return
		}
	case "subscription":
		if action.CustomerCode != "" {
			_, err = a.db.Exec(r.Context(), `UPDATE subscriptions SET status=$2,paystack_subscription_code=coalesce(nullif($3,''),paystack_subscription_code),paystack_email_token=coalesce(nullif($4,''),paystack_email_token),current_period_end=coalesce($5,current_period_end),updated_at=now() WHERE paystack_customer_code=$1`, action.CustomerCode, action.Status, action.SubscriptionCode, action.EmailToken, action.NextPaymentDate)
			if err != nil {
				serverError(a, w, err)
				return
			}
		}
	case "payment_failed":
		if action.CustomerCode != "" {
			_, err = a.db.Exec(r.Context(), `UPDATE subscriptions SET status='past_due',updated_at=now() WHERE paystack_customer_code=$1`, action.CustomerCode)
			if err != nil {
				serverError(a, w, err)
				return
			}
		}
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO webhook_events(id) VALUES($1) ON CONFLICT DO NOTHING`, paystack.Key(event))
	w.WriteHeader(200)
}

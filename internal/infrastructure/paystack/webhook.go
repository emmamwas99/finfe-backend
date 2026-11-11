package paystack

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SignatureConfig holds the secrets used to verify webhooks.
type SignatureConfig struct {
	WebhookSecret string // whsec_…: current `Paystack-Signature` scheme
	SecretKey     string // sk_…: legacy `x-paystack-signature` scheme
	Tolerance     time.Duration
}

// VerifySignature checks a webhook. Current scheme: `Paystack-Signature: t=<unix>,v1=<kid>:<hex>`
// with hex = HMAC-SHA512(webhookSecret, "<t>." + body). Legacy: `x-paystack-signature` =
// HMAC-SHA512(secretKey, body).
func VerifySignature(raw []byte, h http.Header, cfg SignatureConfig, now time.Time) bool {
	if cfg.Tolerance == 0 {
		cfg.Tolerance = 5 * time.Minute
	}
	if cur := h.Get("Paystack-Signature"); cur != "" && cfg.WebhookSecret != "" {
		var t string
		var sigs []string
		for _, part := range strings.Split(cur, ",") {
			k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
			switch k {
			case "t":
				t = v
			case "v1":
				if _, after, ok := strings.Cut(v, ":"); ok {
					v = after
				}
				sigs = append(sigs, strings.ToLower(v))
			}
		}
		ts, err := strconv.ParseInt(t, 10, 64)
		if err != nil || math.Abs(float64(now.Unix()-ts)) > cfg.Tolerance.Seconds() {
			return false
		}
		want := hmacHex(cfg.WebhookSecret, []byte(t+"."), raw)
		for _, s := range sigs {
			if hmac.Equal([]byte(s), []byte(want)) {
				return true
			}
		}
		return false
	}
	if legacy := h.Get("X-Paystack-Signature"); legacy != "" && cfg.SecretKey != "" {
		return hmac.Equal([]byte(strings.ToLower(legacy)), []byte(hmacHex(cfg.SecretKey, raw)))
	}
	return false
}

func hmacHex(key string, parts ...[]byte) string {
	m := hmac.New(sha512.New, []byte(key))
	for _, p := range parts {
		m.Write(p)
	}
	return hex.EncodeToString(m.Sum(nil))
}

// Event is a Paystack webhook (or a verify response wrapped as charge.success).
type Event struct {
	Event string    `json:"event"`
	Data  EventData `json:"data"`
}

type EventData struct {
	ID        json.Number `json:"id"`
	Status    string      `json:"status"`
	Reference string      `json:"reference"`
	Amount    int64       `json:"amount"` // minor units
	Currency  string      `json:"currency"`
	Channel   string      `json:"channel"`
	PaidAt    string      `json:"paid_at"`
	Customer  struct {
		Email        string `json:"email"`
		CustomerCode string `json:"customer_code"`
	} `json:"customer"`
	Metadata         json.RawMessage `json:"metadata"`
	Plan             json.RawMessage `json:"plan"`
	SubscriptionCode string          `json:"subscription_code"`
	EmailToken       string          `json:"email_token"`
	NextPaymentDate  string          `json:"next_payment_date"`
	InvoiceCode      string          `json:"invoice_code"`
	Subscription     struct {
		SubscriptionCode string `json:"subscription_code"`
	} `json:"subscription"`
}

// Action is what an event means for us.
type Action struct {
	Type             string // payment | subscription | payment_failed | ignore
	Reason           string
	Reference        string
	Email            string
	AmountMinor      int64
	Period           Period
	Channel          string
	PaidAt           time.Time
	CustomerCode     string
	Recurring        bool
	SubscriptionCode string
	EmailToken       string
	Status           string // active | non_renewing | cancelled
	NextPaymentDate  *time.Time
}

// Key de-duplicates resent events.
func Key(e Event) string {
	id := e.Data.ID.String()
	for _, alt := range []string{e.Data.Reference, e.Data.SubscriptionCode, e.Data.InvoiceCode} {
		if id == "" {
			id = alt
		}
	}
	return fmt.Sprintf("%s:%s:%s", e.Event, id, e.Data.Status)
}

// Map converts an event into an action.
func Map(e Event, now time.Time) Action {
	d := e.Data
	email := strings.ToLower(d.Customer.Email)
	switch e.Event {
	case "charge.success":
		if d.Status != "" && d.Status != "success" {
			return Action{Type: "ignore", Reason: "status " + d.Status}
		}
		if d.Currency != "" && d.Currency != Currency {
			return Action{Type: "ignore", Reason: "currency " + d.Currency}
		}
		plan := parsePlan(d.Plan)
		period := metaPeriod(d.Metadata)
		if period == "" && plan != nil {
			period = plan.period()
		}
		if period == "" {
			period = PeriodFromAmount(d.Amount)
		}
		paid := now
		if t, err := time.Parse(time.RFC3339, d.PaidAt); err == nil {
			paid = t
		}
		return Action{Type: "payment", Reference: d.Reference, Email: email, AmountMinor: d.Amount, Period: period,
			Channel: d.Channel, PaidAt: paid, CustomerCode: d.Customer.CustomerCode, Recurring: plan != nil}
	case "subscription.create", "subscription.enable", "subscription.not_renew", "subscription.disable":
		a := Action{Type: "subscription", Email: email, CustomerCode: d.Customer.CustomerCode, SubscriptionCode: d.SubscriptionCode, EmailToken: d.EmailToken}
		switch e.Event {
		case "subscription.not_renew":
			a.Status = "non_renewing"
		case "subscription.disable":
			a.Status = "cancelled"
		default:
			a.Status = "active"
		}
		if e.Event != "subscription.disable" {
			if t, err := parseTime(d.NextPaymentDate); err == nil {
				a.NextPaymentDate = &t
			}
		}
		return a
	case "invoice.payment_failed":
		return Action{Type: "payment_failed", Email: email, CustomerCode: d.Customer.CustomerCode, SubscriptionCode: d.Subscription.SubscriptionCode}
	}
	return Action{Type: "ignore", Reason: "unhandled " + e.Event}
}

type planInfo struct {
	PlanCode string `json:"plan_code"`
	Interval string `json:"interval"`
}

func (p planInfo) period() Period {
	switch p.Interval {
	case "annually", "yearly":
		return Yearly
	case "monthly":
		return Monthly
	}
	return ""
}

func parsePlan(raw json.RawMessage) *planInfo {
	var p planInfo
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p.PlanCode == "" {
		return nil
	}
	return &p
}

// metaPeriod reads metadata.period; Paystack may send metadata as an object or a JSON string.
func metaPeriod(raw json.RawMessage) Period {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil || json.Unmarshal([]byte(s), &m) != nil {
			return ""
		}
	}
	switch m["period"] {
	case "yearly":
		return Yearly
	case "monthly":
		return Monthly
	}
	return ""
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse(time.DateOnly, s)
}

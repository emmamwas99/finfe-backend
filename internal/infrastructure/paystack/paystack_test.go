package paystack

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

var body = []byte(`{"event":"charge.success","data":{"reference":"ff_x","amount":29900}}`)

func sign(key string, parts ...[]byte) string {
	m := hmac.New(sha512.New, []byte(key))
	for _, p := range parts {
		m.Write(p)
	}
	return hex.EncodeToString(m.Sum(nil))
}

func TestSignatures(t *testing.T) {
	const secret = "whsec_test123"
	ts := int64(1_800_000_000)
	now := time.Unix(ts+10, 0)
	h := http.Header{}
	h.Set("Paystack-Signature", "t="+strconv.FormatInt(ts, 10)+",v1=whk_abc:"+sign(secret, []byte(strconv.FormatInt(ts, 10)+"."), body))
	if !VerifySignature(body, h, SignatureConfig{WebhookSecret: secret}, now) {
		t.Fatal("current scheme should verify")
	}
	if VerifySignature(body, h, SignatureConfig{WebhookSecret: secret}, time.Unix(ts+301, 0)) {
		t.Fatal("stale timestamp must fail")
	}
	if VerifySignature([]byte(strings.Replace(string(body), "29900", "1", 1)), h, SignatureConfig{WebhookSecret: secret}, now) {
		t.Fatal("tampered body must fail")
	}
	if VerifySignature(body, h, SignatureConfig{WebhookSecret: "whsec_other"}, now) || VerifySignature(body, http.Header{}, SignatureConfig{WebhookSecret: secret}, now) {
		t.Fatal("wrong secret / missing header must fail")
	}
	// rotation: any matching v1 passes
	rot := http.Header{}
	rot.Set("Paystack-Signature", "t="+strconv.FormatInt(ts, 10)+",v1=whk_old:"+strings.Repeat("0", 128)+",v1=whk_new:"+sign("whsec_new", []byte(strconv.FormatInt(ts, 10)+"."), body))
	if !VerifySignature(body, rot, SignatureConfig{WebhookSecret: "whsec_new"}, now) {
		t.Fatal("rotation")
	}
	legacy := http.Header{}
	legacy.Set("X-Paystack-Signature", sign("sk_test_abc", body))
	if !VerifySignature(body, legacy, SignatureConfig{SecretKey: "sk_test_abc"}, now) || VerifySignature(body, legacy, SignatureConfig{SecretKey: "sk_wrong"}, now) {
		t.Fatal("legacy scheme")
	}
}

func event(t *testing.T, s string) Event {
	var e Event
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestMapping(t *testing.T) {
	now := time.Now()
	a := Map(event(t, `{"event":"charge.success","data":{"status":"success","reference":"ff_monthly_1","amount":29900,"currency":"KES","channel":"mobile_money","paid_at":"2026-11-11T10:00:00Z","customer":{"email":"A@B.co","customer_code":"CUS_1"},"metadata":"{\"period\":\"monthly\"}"}}`), now)
	if a.Type != "payment" || a.AmountMinor != 29900 || a.Period != Monthly || a.Recurring || a.Email != "a@b.co" || a.CustomerCode != "CUS_1" {
		t.Fatalf("%+v", a)
	}
	y := Map(event(t, `{"event":"charge.success","data":{"status":"success","reference":"r2","amount":299900,"currency":"KES","channel":"card","plan":{"plan_code":"PLN_1","interval":"annually"},"customer":{"email":"x@y.z"}}}`), now)
	if y.Period != Yearly || !y.Recurring {
		t.Fatalf("%+v", y)
	}
	if Map(event(t, `{"event":"charge.success","data":{"status":"failed"}}`), now).Type != "ignore" ||
		Map(event(t, `{"event":"charge.success","data":{"status":"success","currency":"NGN"}}`), now).Type != "ignore" {
		t.Fatal("ignore failed and foreign-currency charges")
	}
	s := Map(event(t, `{"event":"subscription.create","data":{"subscription_code":"SUB_1","email_token":"t","next_payment_date":"2026-12-11T00:00:00Z","customer":{"email":"a@b.co"}}}`), now)
	if s.Type != "subscription" || s.Status != "active" || s.SubscriptionCode != "SUB_1" || s.NextPaymentDate == nil {
		t.Fatalf("%+v", s)
	}
	if Map(event(t, `{"event":"subscription.not_renew","data":{}}`), now).Status != "non_renewing" || Map(event(t, `{"event":"subscription.disable","data":{}}`), now).Status != "cancelled" {
		t.Fatal("lifecycle")
	}
	f := Map(event(t, `{"event":"invoice.payment_failed","data":{"subscription":{"subscription_code":"SUB_1"}}}`), now)
	if f.Type != "payment_failed" || f.SubscriptionCode != "SUB_1" {
		t.Fatalf("%+v", f)
	}
	e := event(t, `{"event":"charge.success","data":{"id":42,"status":"success"}}`)
	if Key(e) != Key(event(t, `{"event":"charge.success","data":{"id":42,"status":"success"}}`)) || Key(e) != "charge.success:42:success" {
		t.Fatal("stable keys")
	}
}

func TestPlans(t *testing.T) {
	if PeriodFromAmount(299_00) != Monthly || PeriodFromAmount(2_999_00) != Yearly {
		t.Fatal("period from amount")
	}
	if !AmountMatches(299_00, Monthly) || AmountMatches(1, Monthly) || AmountMatches(299_00, Yearly) {
		t.Fatal("amount guard")
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	at := func(d time.Duration) *time.Time { x := now.Add(d); return &x }
	card := "card"
	trial := &Subscription{Status: "trialing", TrialEndsAt: at(3 * day)}
	if !IsPro(trial, now) || !IsTrial(trial, now) || IsPro(trial, now.Add(4*day)) {
		t.Fatal("trial")
	}
	paid := &Subscription{Status: "active", TrialEndsAt: at(-day), CurrentPeriodEnd: at(20 * day)}
	if !IsPro(paid, now) || IsTrial(paid, now) {
		t.Fatal("paid")
	}
	grace := &Subscription{Status: "past_due", Billing: &card, CurrentPeriodEnd: at(-day)}
	if !IsPro(grace, now) || !ProUntil(grace).Equal(now.Add(2*day)) || IsPro(grace, now.Add(3*day)) {
		t.Fatal("grace period")
	}
	if IsPro(nil, now) {
		t.Fatal("no subscription is Free")
	}
	// remaining trial time is kept: paid month starts when the trial ends
	if got := Extend(now, at(5*day), nil, Monthly); !got.Equal(now.Add(5*day).AddDate(0, 1, 0)) {
		t.Fatalf("extend %v", got)
	}
	if got := Extend(now, nil, at(10*day), Yearly); !got.Equal(now.Add(10*day).AddDate(1, 0, 0)) {
		t.Fatalf("stack %v", got)
	}
}

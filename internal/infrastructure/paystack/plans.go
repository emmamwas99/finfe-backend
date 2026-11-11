// Package paystack ports Finfe's billing rules and Paystack integration from the former
// Supabase Edge Functions: prices, Pro access, webhook signatures, event mapping and the API client.
package paystack

import "time"

const (
	Currency = "KES"
	// GraceDays keeps Pro after a failed card renewal while the customer fixes the card.
	GraceDays = 3
)

type Period string

const (
	Monthly Period = "monthly"
	Yearly  Period = "yearly"
)

// PriceMinor of each period, in KES cents. Keep in sync with the web app's src/lib/plans.ts.
var PriceMinor = map[Period]int64{Monthly: 299_00, Yearly: 2_999_00}

// Subscription is the subset of the subscriptions row needed for access decisions.
type Subscription struct {
	Status           string
	TrialEndsAt      *time.Time
	CurrentPeriodEnd *time.Time
	Billing          *string
}

// ProUntil is when Pro access ends (trial or paid, plus grace for failed card renewals).
func ProUntil(s *Subscription) *time.Time {
	if s == nil {
		return nil
	}
	var best time.Time
	if s.TrialEndsAt != nil {
		best = *s.TrialEndsAt
	}
	if s.CurrentPeriodEnd != nil {
		end := *s.CurrentPeriodEnd
		if s.Status == "past_due" && s.Billing != nil && *s.Billing == "card" {
			end = end.Add(GraceDays * 24 * time.Hour)
		}
		if end.After(best) {
			best = end
		}
	}
	if best.IsZero() {
		return nil
	}
	return &best
}

// IsPro reports whether the subscription gives Pro features at now.
func IsPro(s *Subscription, now time.Time) bool {
	u := ProUntil(s)
	return u != nil && u.After(now)
}

// IsTrial reports an active trial with no paid period running.
func IsTrial(s *Subscription, now time.Time) bool {
	if s == nil || s.TrialEndsAt == nil || !s.TrialEndsAt.After(now) {
		return false
	}
	return s.CurrentPeriodEnd == nil || !s.CurrentPeriodEnd.After(now)
}

// PeriodFromAmount picks the closest price (yearly ≈ 10× monthly, so it's unambiguous).
func PeriodFromAmount(amountMinor int64) Period {
	d := func(a, b int64) int64 {
		if a > b {
			return a - b
		}
		return b - a
	}
	if d(amountMinor, PriceMinor[Yearly]) < d(amountMinor, PriceMinor[Monthly]) {
		return Yearly
	}
	return Monthly
}

// AmountMatches rejects charges below the price (protects against tampered amounts).
func AmountMatches(amountMinor int64, p Period) bool { return amountMinor >= PriceMinor[p] }

// Extend returns the new period end: paid time starts at the later of now, the trial end and the current period end.
func Extend(now time.Time, trialEnd, periodEnd *time.Time, p Period) time.Time {
	base := now
	if trialEnd != nil && trialEnd.After(base) {
		base = *trialEnd
	}
	if periodEnd != nil && periodEnd.After(base) {
		base = *periodEnd
	}
	if p == Yearly {
		return base.AddDate(1, 0, 0)
	}
	return base.AddDate(0, 1, 0)
}

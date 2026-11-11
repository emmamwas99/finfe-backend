// Package plan is the server-side port of the web app's monthly plan (src/lib/calc.ts):
//
//	income − planned payments = left after payments
//	left after payments − other spending = remaining
//
// A paid payment counts what was actually paid; an unpaid one counts its planned amount.
package plan

import (
	"sort"

	"github.com/google/uuid"

	"finfe/internal/domain/calendar"
)

// Payment is a planned payment rule.
type Payment struct {
	ID          uuid.UUID
	Name        string
	AmountMinor int64
	DueDay      int
	Months      []int // 1–12; empty = every month
	OnlyMonth   *calendar.Month
	FromMonth   *calendar.Month
	Overrides   map[string]int64 // "YYYY-MM" → amount
	Skips       map[string]bool
	LoanID      *uuid.UUID
	GoalID      *uuid.UUID
	Active      bool
}

// Due reports whether the payment applies in month m.
func (p Payment) Due(m calendar.Month) bool {
	if !p.Active {
		return false
	}
	if p.OnlyMonth != nil {
		return p.OnlyMonth.Equal(m)
	}
	if p.FromMonth != nil && m.Before(*p.FromMonth) {
		return false
	}
	if p.Skips[m.String()] {
		return false
	}
	if len(p.Months) == 0 {
		return true
	}
	for _, n := range p.Months {
		if n == m.Number() {
			return true
		}
	}
	return false
}

// Amount is the planned amount for month m (one-month overrides win).
func (p Payment) Amount(m calendar.Month) int64 {
	if v, ok := p.Overrides[m.String()]; ok {
		return v
	}
	return p.AmountMinor
}

// Expense is the slice of an expense the plan needs.
type Expense struct {
	ID               uuid.UUID
	AmountMinor      int64
	LoanID           *uuid.UUID
	GoalID           *uuid.UUID
	PlannedPaymentID *uuid.UUID
}

// Status of one planned payment in a month.
type Status struct {
	Payment      Payment
	PlannedMinor int64
	Paid         bool
	PaidMinor    int64
	ExpenseIDs   []uuid.UUID
}

// Match finds which expenses pay which due payments:
//  1. exact: expenses recorded against the payment;
//  2. loose: a loan/goal payment recorded elsewhere counts for the first unpaid payment
//     linked to the same loan/goal.
func Match(due []Payment, m calendar.Month, expenses []Expense) []Status {
	sort.SliceStable(due, func(i, j int) bool {
		if due[i].DueDay != due[j].DueDay {
			return due[i].DueDay < due[j].DueDay
		}
		return due[i].Name < due[j].Name
	})
	idx := map[uuid.UUID]int{}
	out := make([]Status, len(due))
	for i, p := range due {
		idx[p.ID] = i
		out[i] = Status{Payment: p, PlannedMinor: p.Amount(m)}
	}
	add := func(i int, e Expense) {
		out[i].ExpenseIDs = append(out[i].ExpenseIDs, e.ID)
		out[i].PaidMinor += e.AmountMinor
		out[i].Paid = true
	}
	exact := map[uuid.UUID]bool{}
	for _, e := range expenses {
		if e.PlannedPaymentID != nil {
			if i, ok := idx[*e.PlannedPaymentID]; ok {
				add(i, e)
				exact[e.ID] = true
			}
		}
	}
	for _, e := range expenses {
		if exact[e.ID] {
			continue
		}
		for i, s := range out {
			if len(s.ExpenseIDs) > 0 {
				continue
			}
			if (s.Payment.LoanID != nil && e.LoanID != nil && *s.Payment.LoanID == *e.LoanID) ||
				(s.Payment.GoalID != nil && e.GoalID != nil && *s.Payment.GoalID == *e.GoalID) {
				add(i, e)
				break
			}
		}
	}
	return out
}

// Income rules ------------------------------------------------------------

// IncomeSettings are the user's expected-income preferences.
type IncomeSettings struct {
	DefaultMinor int64
	From         map[string]int64 // "YYYY-MM" → amount from that month on
	Only         map[string]int64 // "YYYY-MM" → amount for that month only
}

// Expected income for month m: one-month override → latest "from" change → usual amount.
func (s IncomeSettings) Expected(m calendar.Month) int64 {
	if v, ok := s.Only[m.String()]; ok {
		return v
	}
	best := ""
	for k := range s.From {
		if k <= m.String() && k > best {
			best = k
		}
	}
	if best != "" {
		return s.From[best]
	}
	return s.DefaultMinor
}

// Planned income: past months use what was received (or the expectation if nothing was
// recorded); the current and future months use the larger of received and expected.
func (s IncomeSettings) Planned(m, current calendar.Month, earnedMinor int64) int64 {
	exp := s.Expected(m)
	if m.Before(current) {
		if earnedMinor > 0 {
			return earnedMinor
		}
		return exp
	}
	return max(earnedMinor, exp)
}

// Month plan --------------------------------------------------------------

// Result is a month's plan.
type Result struct {
	Month           calendar.Month
	IncomeMinor     int64
	Payments        []Status
	PaymentsMinor   int64 // paid amounts for paid items + planned amounts for unpaid ones
	PaidMinor       int64
	ToPayMinor      int64
	OtherSpentMinor int64
	AfterPayments   int64
	RemainingMinor  int64
}

// Compute the plan for month m.
func Compute(m, current calendar.Month, income IncomeSettings, earnedMinor int64, payments []Payment, expenses []Expense) Result {
	var due []Payment
	for _, p := range payments {
		if p.Due(m) {
			due = append(due, p)
		}
	}
	st := Match(due, m, expenses)
	r := Result{Month: m, IncomeMinor: income.Planned(m, current, earnedMinor), Payments: st}
	matched := map[uuid.UUID]bool{}
	for _, s := range st {
		for _, id := range s.ExpenseIDs {
			matched[id] = true
		}
		if s.Paid {
			r.PaymentsMinor += s.PaidMinor
			r.PaidMinor += s.PaidMinor
		} else {
			r.PaymentsMinor += s.PlannedMinor
			r.ToPayMinor += s.PlannedMinor
		}
	}
	for _, e := range expenses {
		if !matched[e.ID] {
			r.OtherSpentMinor += e.AmountMinor
		}
	}
	r.AfterPayments = r.IncomeMinor - r.PaymentsMinor
	r.RemainingMinor = r.AfterPayments - r.OtherSpentMinor
	return r
}

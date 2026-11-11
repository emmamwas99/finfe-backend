package financialhealth

import (
	"testing"
	"time"

	"finfe/internal/domain/calendar"
	"finfe/internal/domain/debt"
)

var today = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)

func month(m string, income, spent int64, kinds map[string]int64) MonthData {
	return MonthData{Month: calendar.MustMonth(m), IncomeMinor: income, EarnedMinor: income, SpentMinor: spent, ByKind: kinds, Entries: 10}
}

func byID(r Result) map[string]Check {
	out := map[string]Check{}
	for _, c := range r.Checks {
		out[c.ID] = c
	}
	return out
}

func healthy() Input {
	k := map[string]int64{KindNeed: 40_000_00, KindWant: 12_000_00, KindSaving: 10_000_00, KindDebt: 8_000_00}
	return Input{
		Currency: "KES", Today: today,
		Window:    []MonthData{month("2026-09", 100_000_00, 70_000_00, k), month("2026-10", 100_000_00, 70_000_00, k), month("2026-11", 100_000_00, 70_000_00, k)},
		OpenLoans: 1, DebtTotalMinor: 100_000_00, MonthlyDebtPayments: 8_000_00, ExpectedIncomeNow: 100_000_00, MonthsToDebtFree: 14,
		EmergencyFundMinor: 200_000_00, EmergencyFundAccounts: 1,
		Upcoming:     []UpcomingMonth{{Month: calendar.MustMonth("2026-12"), IncomeMinor: 100_000_00, PaymentsMinor: 30_000_00}},
		BudgetsTotal: 3, EntriesLast14Days: 12,
	}
}

func TestEmpty(t *testing.T) {
	r := Evaluate(Input{Currency: "KES", Today: today})
	if !r.Empty || len(r.Checks) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestHealthyHouseholdIsAllGood(t *testing.T) {
	r := Evaluate(healthy())
	if r.Summary.Bad != 0 || r.Summary.Warn != 0 || len(r.Actions) != 0 {
		for _, c := range r.Checks {
			t.Logf("%s %s %s", c.ID, c.Status, c.Metric)
		}
		t.Fatalf("summary %+v", r.Summary)
	}
	c := byID(r)
	if c["emergency_fund"].Value < 4.1 || c["emergency_fund"].Value > 4.2 { // 200k ÷ 48k essentials
		t.Fatalf("emergency months %v", c["emergency_fund"].Value)
	}
	if c["savings_rate"].Value != 0.1 || c["living_within_income"].Value != 0.7 {
		t.Fatalf("ratios %v %v", c["savings_rate"].Value, c["living_within_income"].Value)
	}
}

func TestThresholds(t *testing.T) {
	in := healthy()
	k := map[string]int64{KindNeed: 50_000_00, KindWant: 50_000_00, KindDebt: 10_000_00}
	in.Window = []MonthData{month("2026-11", 100_000_00, 110_000_00, k)}
	in.Window[0].BorrowedMinor = 20_000_00
	in.MonthlyDebtPayments = 55_000_00
	in.EmergencyFundMinor = 10_000_00
	in.MonthsToDebtFree = debt.Never
	in.BudgetsOver = []string{"Food", "Fun", "Transport"}
	in.Upcoming = []UpcomingMonth{{Month: calendar.MustMonth("2027-01"), IncomeMinor: 100_000_00, PaymentsMinor: 130_000_00}}
	in.EntriesLast14Days = 1
	r := Evaluate(in)
	c := byID(r)
	for _, id := range []string{"living_within_income", "borrowing", "debt_burden", "savings_rate", "emergency_fund", "wants", "budgets", "upcoming", "debt_free", "consistency"} {
		if c[id].Status != Bad {
			t.Errorf("%s: %s (%s)", id, c[id].Status, c[id].Metric)
		}
	}
	if r.Checks[0].Status != Bad || len(r.Actions) != 3 {
		t.Fatal("worst first, three actions")
	}
	if c["upcoming"].Advice == "" || c["debt_burden"].Value != 0.55 {
		t.Fatalf("upcoming %+v burden %v", c["upcoming"], c["debt_burden"].Value)
	}
}

func TestWarnBands(t *testing.T) {
	in := healthy()
	k := map[string]int64{KindNeed: 50_000_00, KindWant: 35_000_00, KindSaving: 5_000_00}
	in.Window = []MonthData{month("2026-11", 100_000_00, 95_000_00, k)}
	in.Window[0].BorrowedMinor = 5_000_00
	in.MonthlyDebtPayments = 40_000_00
	in.EmergencyFundMinor = 100_000_00
	in.EntriesLast14Days = 4
	c := byID(Evaluate(in))
	for _, id := range []string{"living_within_income", "borrowing", "debt_burden", "savings_rate", "emergency_fund", "wants", "consistency"} {
		if c[id].Status != Warn {
			t.Errorf("%s: %s (%s)", id, c[id].Status, c[id].Metric)
		}
	}
}

func TestNoIncomeButSpendingIsOverspending(t *testing.T) {
	in := healthy()
	in.Window = []MonthData{{Month: calendar.MustMonth("2026-11"), SpentMinor: 5_000_00, Entries: 3, ByKind: map[string]int64{KindNeed: 5_000_00}}}
	if byID(Evaluate(in))["living_within_income"].Status != Bad {
		t.Fatal("spending with no income must be flagged")
	}
}

func TestDirection(t *testing.T) {
	series := []MonthData{
		{Month: calendar.MustMonth("2026-08"), IncomeMinor: 100, SpentMinor: 90, Entries: 1},
		{Month: calendar.MustMonth("2026-09"), IncomeMinor: 100, SpentMinor: 90, Entries: 1},
		{Month: calendar.MustMonth("2026-10"), IncomeMinor: 100, SpentMinor: 70, Entries: 1},
		{Month: calendar.MustMonth("2026-11"), IncomeMinor: 100, SpentMinor: 70, Entries: 1},
	}
	if ch, ok := Direction(series); !ok || ch != 20 {
		t.Fatalf("change %d ok %v", ch, ok)
	}
	if _, ok := Direction(series[:1]); ok {
		t.Fatal("needs two months")
	}
}

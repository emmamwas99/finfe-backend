package plan

import (
	"testing"

	"github.com/google/uuid"

	"finfe/internal/domain/calendar"
)

var nov = calendar.MustMonth("2026-11")

func ptrM(s string) *calendar.Month { m := calendar.MustMonth(s); return &m }

func TestDue(t *testing.T) {
	base := Payment{Active: true}
	if !base.Due(nov) {
		t.Fatal("repeating")
	}
	if (Payment{}).Due(nov) {
		t.Fatal("inactive")
	}
	if (Payment{Active: true, Months: []int{1, 5, 9}}).Due(nov) || !(Payment{Active: true, Months: []int{1, 5, 9}}).Due(calendar.MustMonth("2027-01")) {
		t.Fatal("months filter")
	}
	if !(Payment{Active: true, OnlyMonth: ptrM("2026-11")}).Due(nov) || (Payment{Active: true, OnlyMonth: ptrM("2026-11")}).Due(nov.Next()) {
		t.Fatal("one-off")
	}
	if (Payment{Active: true, FromMonth: ptrM("2027-01")}).Due(nov) {
		t.Fatal("from month")
	}
	if (Payment{Active: true, Skips: map[string]bool{"2026-11": true}}).Due(nov) {
		t.Fatal("skip")
	}
}

func TestIncome(t *testing.T) {
	s := IncomeSettings{DefaultMinor: 85_000_00, From: map[string]int64{"2026-12": 264_000_00, "2027-03": 300_000_00}, Only: map[string]int64{"2026-11": 388_000_00}}
	cases := map[string]int64{"2026-10": 85_000_00, "2026-11": 388_000_00, "2026-12": 264_000_00, "2027-02": 264_000_00, "2027-04": 300_000_00}
	for m, want := range cases {
		if got := s.Expected(calendar.MustMonth(m)); got != want {
			t.Errorf("%s: %d want %d", m, got, want)
		}
	}
	plain := IncomeSettings{DefaultMinor: 85_000_00}
	oct := calendar.MustMonth("2026-10")
	if plain.Planned(oct, nov, 70_000_00) != 70_000_00 || plain.Planned(oct, nov, 0) != 85_000_00 {
		t.Fatal("past months")
	}
	if plain.Planned(nov, nov, 40_000_00) != 85_000_00 || plain.Planned(nov, nov, 90_000_00) != 90_000_00 {
		t.Fatal("current month")
	}
}

func TestComputeMatchesWebApp(t *testing.T) {
	loan := uuid.New()
	rent := Payment{ID: uuid.New(), Name: "rent", AmountMinor: 18_000_00, DueDay: 5, Active: true}
	sacco := Payment{ID: uuid.New(), Name: "sacco", AmountMinor: 8_000_00, DueDay: 5, Active: true, LoanID: &loan}
	fees := Payment{ID: uuid.New(), Name: "fees", AmountMinor: 7_000_00, DueDay: 5, Active: true, Overrides: map[string]int64{"2026-11": 9_000_00}}
	exps := []Expense{
		{ID: uuid.New(), AmountMinor: 18_500_00, PlannedPaymentID: &rent.ID}, // exact match, paid more than planned
		{ID: uuid.New(), AmountMinor: 8_000_00, LoanID: &loan},               // loose match via the loan
		{ID: uuid.New(), AmountMinor: 6_000_00},                              // other spending
	}
	r := Compute(nov, nov, IncomeSettings{DefaultMinor: 85_000_00}, 85_000_00, []Payment{rent, sacco, fees}, exps)
	if r.PaymentsMinor != 18_500_00+8_000_00+9_000_00 || r.PaidMinor != 26_500_00 || r.ToPayMinor != 9_000_00 {
		t.Fatalf("%+v", r)
	}
	if r.OtherSpentMinor != 6_000_00 || r.AfterPayments != 85_000_00-35_500_00 || r.RemainingMinor != 85_000_00-35_500_00-6_000_00 {
		t.Fatalf("%+v", r)
	}
}

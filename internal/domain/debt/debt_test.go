package debt

import "testing"

var loans = []Loan{
	{BalanceMinor: 180_000_00, PaymentMinor: 9_000_00, RateBp: 1600},
	{BalanceMinor: 60_000_00, PaymentMinor: 4_000_00, RateBp: 1200},
	{BalanceMinor: 12_000_00, PaymentMinor: 3_000_00, RateBp: 6000},
}

func TestMonthsToClear(t *testing.T) {
	cases := []struct {
		bal, pay, rate int64
		want           int
	}{
		{0, 100, 0, 0},
		{1000, 0, 0, Never},
		{12_000_00, 1_000_00, 0, 12},
		{12_000_01, 1_000_00, 0, 13},
		{100_000_00, 1_000_00, 1200, Never}, // payment == interest
		{120_000_00, 6_000_00, 1400, 23},    // same as the web app's float engine
		{120_000_00, 8_000_00, 1400, 17},
		{162_000_00, 9_000_00, 1600, 21},
	}
	for _, c := range cases {
		if got := MonthsToClear(c.bal, c.pay, c.rate); got != c.want {
			t.Errorf("MonthsToClear(%d,%d,%d)=%d want %d", c.bal, c.pay, c.rate, got, c.want)
		}
	}
}

func TestMonthlyInterestRounds(t *testing.T) {
	if got := MonthlyInterest(180_000_00, 1600); got != 2_400_00 {
		t.Fatalf("got %d", got)
	}
	if got := MonthlyInterest(1, 1600); got != 0 {
		t.Fatalf("got %d", got)
	}
}

func TestSimulateExtraAndRollover(t *testing.T) {
	base := Simulate(loans, Options{MaxMonths: 600})
	extra := Simulate(loans, Options{MaxMonths: 600, ExtraPerMonthMinor: 2_000_00})
	roll := Simulate(loans, Options{MaxMonths: 600, Rollover: true})
	if base.Months == Never || extra.Months >= base.Months || extra.InterestMinor >= base.InterestMinor {
		t.Fatalf("extra should be faster and cheaper: base=%+v extra=%+v", base.Months, extra.Months)
	}
	if roll.Months > base.Months || roll.InterestMinor > base.InterestMinor {
		t.Fatal("rollover is never slower")
	}
	// avalanche: the 60% loan clears before the others when paying extra
	if !(extra.ClearedAt[2] < extra.ClearedAt[0] && extra.ClearedAt[2] < extra.ClearedAt[1]) {
		t.Fatalf("cleared at %v", extra.ClearedAt)
	}
	if base.TotalsMinor[0] != 252_000_00 || base.TotalsMinor[len(base.TotalsMinor)-1] != 0 {
		t.Fatal("totals start at today's balance and end at zero")
	}
}

func TestSimulateNeverAndEmpty(t *testing.T) {
	s := Simulate([]Loan{{BalanceMinor: 100_000_00, PaymentMinor: 1_500_00, RateBp: 2400}}, Options{MaxMonths: 600})
	if s.Months != Never || s.ClearedAt[0] != Never {
		t.Fatalf("got %+v", s)
	}
	if e := Simulate(nil, Options{}); e.Months != 0 || len(e.TotalsMinor) != 1 {
		t.Fatalf("empty: %+v", e)
	}
}

func TestInterestEqualsPaidMinusPrincipal(t *testing.T) {
	s := Simulate([]Loan{{BalanceMinor: 120_000_00, PaymentMinor: 8_000_00, RateBp: 1400}}, Options{MaxMonths: 600})
	// 16 full payments + a partial last one: paid − principal = interest
	if s.InterestMinor < 12_000_00 || s.InterestMinor > 13_500_00 {
		t.Fatalf("interest %d", s.InterestMinor)
	}
}

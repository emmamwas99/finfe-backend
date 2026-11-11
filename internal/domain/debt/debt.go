// Package debt projects loan payoff with integer money. Interest is charged monthly on the
// reducing balance: interest = round(balance × annualRateBp ÷ 120 000). It is the server-side
// twin of the web app's debt engine (src/lib/calculators/debt.ts).
package debt

import "finfe/internal/domain/money"

// Never means the balance does not clear within the simulation horizon.
const Never = -1

// Loan is one debt to simulate.
type Loan struct {
	BalanceMinor int64
	PaymentMinor int64
	RateBp       int64 // annual interest rate in basis points (1600 = 16%)
}

// Options configure a simulation.
type Options struct {
	ExtraPerMonthMinor int64
	MaxMonths          int
	// Rollover moves a cleared loan's monthly payment onto the remaining loans.
	Rollover bool
}

// Result of a simulation.
type Result struct {
	// TotalsMinor is the total balance at the start of each month; index 0 = today.
	TotalsMinor []int64
	// Months until every balance is cleared, or Never.
	Months int
	// InterestMinor charged over the simulation.
	InterestMinor int64
	// ClearedAt is the month (1-based) each loan clears, or Never.
	ClearedAt []int
}

// MonthlyInterest is the interest one month adds to a balance.
func MonthlyInterest(balanceMinor, rateBp int64) int64 {
	return money.DivRound(balanceMinor*rateBp, 120_000)
}

// MonthsToClear runs the single-loan simulation (exact with the same rounding as Simulate).
func MonthsToClear(balanceMinor, paymentMinor, rateBp int64) int {
	if balanceMinor <= 0 {
		return 0
	}
	if paymentMinor <= 0 || paymentMinor <= MonthlyInterest(balanceMinor, rateBp) {
		return Never
	}
	return Simulate([]Loan{{balanceMinor, paymentMinor, rateBp}}, Options{MaxMonths: 1200}).Months
}

// Simulate projects all loans month by month.
//   - Each loan gets its planned payment; an overpayment in the month it clears becomes extra.
//   - Extra money goes to the highest-interest loan first (avalanche).
//   - With Rollover, cleared loans' payments are added to the extra.
func Simulate(loans []Loan, opt Options) Result {
	if opt.MaxMonths <= 0 {
		opt.MaxMonths = 120
	}
	type st struct{ bal, pay, rate int64 }
	s := make([]st, len(loans))
	cleared := make([]int, len(loans))
	for i, l := range loans {
		s[i] = st{max(0, l.BalanceMinor), max(0, l.PaymentMinor), max(0, l.RateBp)}
		cleared[i] = Never
		if s[i].bal == 0 {
			cleared[i] = 0
		}
	}
	total := func() int64 {
		var t int64
		for _, l := range s {
			t += l.bal
		}
		return t
	}
	// avalanche order: indices sorted by rate, highest first (stable)
	order := make([]int, len(s))
	for i := range order {
		order[i] = i
	}
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && s[order[j]].rate > s[order[j-1]].rate; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}

	res := Result{TotalsMinor: []int64{total()}}
	month := 0
	for ; month < opt.MaxMonths && total() > 0; month++ {
		extra := max(0, opt.ExtraPerMonthMinor)
		if opt.Rollover {
			for _, l := range s {
				if l.bal == 0 {
					extra += l.pay
				}
			}
		}
		for i := range s {
			if s[i].bal == 0 {
				continue
			}
			interest := MonthlyInterest(s[i].bal, s[i].rate)
			res.InterestMinor += interest
			s[i].bal += interest - s[i].pay
			if s[i].bal < 0 {
				extra += -s[i].bal
				s[i].bal = 0
			}
		}
		for _, i := range order {
			if extra <= 0 {
				break
			}
			if s[i].bal == 0 {
				continue
			}
			use := min(extra, s[i].bal)
			s[i].bal -= use
			extra -= use
		}
		for i := range s {
			if s[i].bal == 0 && cleared[i] == Never {
				cleared[i] = month + 1
			}
		}
		res.TotalsMinor = append(res.TotalsMinor, total())
	}
	res.ClearedAt = cleared
	if total() == 0 {
		res.Months = month
	} else {
		res.Months = Never
	}
	return res
}

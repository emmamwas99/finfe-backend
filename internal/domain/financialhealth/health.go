// Package financialhealth is Finfe's financial health engine: pure functions from monthly
// aggregates to explainable checks. No I/O, so every rule is unit-tested.
//
// It deliberately does NOT produce a single opaque score. Each check states its formula,
// its thresholds and what to do next; the summary only counts good / warn / bad checks.
// Thresholds match the web app's "Am I on track?" check (src/lib/insights.ts).
// Formulas are documented in docs/financial-health.md.
package financialhealth

import (
	"fmt"
	"sort"
	"time"

	"finfe/internal/domain/calendar"
	"finfe/internal/domain/money"
)

type Status string

const (
	Good Status = "good"
	Warn Status = "warn"
	Bad  Status = "bad"
)

// Kinds of spending (category kinds).
const (
	KindNeed   = "need"
	KindWant   = "want"
	KindDebt   = "debt"
	KindInvest = "invest"
	KindSaving = "saving"
)

// MonthData is one month of aggregates, in a single currency.
type MonthData struct {
	Month         calendar.Month
	IncomeMinor   int64 // planned income: received, or expected when nothing is recorded yet
	EarnedMinor   int64
	BorrowedMinor int64
	SpentMinor    int64
	ByKind        map[string]int64
	Entries       int64
}

// HasData is true when anything was recorded in the month.
func (m MonthData) HasData() bool { return m.Entries > 0 || m.EarnedMinor > 0 }

// CashFlowMinor = planned income − spending.
func (m MonthData) CashFlowMinor() int64 { return m.IncomeMinor - m.SpentMinor }

// SavingsMinor is money put into savings and investments (saving + invest categories).
func (m MonthData) SavingsMinor() int64 { return m.ByKind[KindSaving] + m.ByKind[KindInvest] }

// UpcomingMonth is a future month's planned income and planned payments.
type UpcomingMonth struct {
	Month         calendar.Month
	IncomeMinor   int64
	PaymentsMinor int64
}

// Input to the engine.
type Input struct {
	Currency string
	Today    time.Time
	// Window: the last 3 months including the current one (oldest first). Months without data are ignored.
	Window []MonthData
	// Debt
	OpenLoans             int
	DebtTotalMinor        int64
	MonthlyDebtPayments   int64 // planned debt payments this month (falls back to loans' monthly payments)
	ExpectedIncomeNow     int64
	MonthsToDebtFree      int // debt.Never (-1) when it never clears
	HighestRateLoan       string
	HighestRateBp         int64
	EmergencyFundMinor    int64
	EmergencyFundAccounts int64
	Upcoming              []UpcomingMonth
	BudgetsTotal          int
	BudgetsOver           []string
	GoalsTotal            int
	GoalsBehind           []string
	EntriesLast14Days     int64
}

// Check is one explained health check.
type Check struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Status  Status  `json:"status"`
	Metric  string  `json:"metric"`
	Value   float64 `json:"value"` // the measured ratio or count behind the metric
	Detail  string  `json:"detail"`
	Advice  string  `json:"advice,omitempty"`
	Formula string  `json:"formula"`
}

// Summary counts checks by status. It is not a score.
type Summary struct {
	Good int `json:"good"`
	Warn int `json:"warn"`
	Bad  int `json:"bad"`
}

// Result of an evaluation.
type Result struct {
	Empty   bool    `json:"empty"`
	Period  string  `json:"period"`
	Checks  []Check `json:"checks"` // worst first
	Summary Summary `json:"summary"`
	Actions []Check `json:"actions"` // up to 3 most urgent checks with advice
	Verdict string  `json:"verdict"`
}

// Totals over the window months that have data.
type Totals struct {
	Months                                   int
	Income, Earned, Borrowed, Spent, Savings int64
	Need, Want, Debt                         int64
}

// Sum aggregates the window.
func Sum(window []MonthData) Totals {
	var t Totals
	for _, m := range window {
		if !m.HasData() {
			continue
		}
		t.Months++
		t.Income += m.IncomeMinor
		t.Earned += m.EarnedMinor
		t.Borrowed += m.BorrowedMinor
		t.Spent += m.SpentMinor
		t.Savings += m.SavingsMinor()
		t.Need += m.ByKind[KindNeed]
		t.Want += m.ByKind[KindWant]
		t.Debt += m.ByKind[KindDebt]
	}
	return t
}

func pct(r float64) string { return fmt.Sprintf("%.0f%%", r*100) }

// Evaluate runs every check.
func Evaluate(in Input) Result {
	t := Sum(in.Window)
	if t.Months == 0 {
		return Result{Empty: true, Checks: []Check{}, Actions: []Check{}, Verdict: "Record your income and a few weeks of spending to see how you're doing."}
	}
	d := func(minor int64) string { return money.Display(minor, in.Currency) }
	period := "this month"
	if t.Months > 1 {
		period = fmt.Sprintf("the last %d months", t.Months)
	}
	n := int64(t.Months)
	var checks []Check

	// 1. Living within income: spent ÷ income.
	{
		r := ratioOrOverspend(t.Spent, t.Income)
		c := Check{ID: "living_within_income", Title: "Living within your income", Value: r, Metric: pct(r) + " of income spent",
			Detail:  fmt.Sprintf("Over %s you spent %s of %s.", period, d(t.Spent), d(t.Income)),
			Formula: "total spending ÷ planned income; good ≤ 90%, watch ≤ 100%, fix > 100%"}
		switch {
		case r <= 0.9:
			c.Status = Good
		case r <= 1:
			c.Status, c.Advice = Warn, "You spend almost everything you earn. Aim to keep at least 10% each month."
		default:
			c.Status, c.Advice = Bad, fmt.Sprintf("You overspend by about %s a month. Cut that much, starting with “wants”.", d((t.Spent-t.Income)/n))
		}
		checks = append(checks, c)
	}

	// 2. Not borrowing to get by: borrowed ÷ income.
	{
		r := money.Ratio(t.Borrowed, t.Income)
		c := Check{ID: "borrowing", Title: "Not borrowing to get by", Value: r,
			Formula: "money borrowed ÷ planned income; good = 0, watch ≤ 10%, fix > 10%"}
		if t.Borrowed == 0 {
			c.Status, c.Metric, c.Detail = Good, "No new loans", fmt.Sprintf("You haven't taken a new loan over %s.", period)
		} else {
			c.Metric, c.Detail = d(t.Borrowed)+" borrowed", fmt.Sprintf("You took %s in new loans over %s.", d(t.Borrowed), period)
			c.Advice = "Stop new loans. Build an emergency fund of one month's needs so surprises don't force you to borrow."
			if r <= 0.1 {
				c.Status = Warn
			} else {
				c.Status = Bad
			}
		}
		checks = append(checks, c)
	}

	// 3. Debt going down: repaid vs borrowed.
	if in.OpenLoans > 0 || t.Borrowed > 0 {
		c := Check{ID: "debt_direction", Title: "Debt is going down", Value: money.Ratio(t.Debt, max(t.Borrowed, 1)),
			Metric:  fmt.Sprintf("%s repaid vs %s borrowed", d(t.Debt), d(t.Borrowed)),
			Detail:  "Total debt now: " + d(in.DebtTotalMinor) + ".",
			Formula: "debt repayments vs new borrowing over the window; good when repayments exceed borrowing"}
		switch {
		case t.Debt > t.Borrowed && t.Debt > 0:
			c.Status = Good
			if in.HighestRateLoan != "" && in.HighestRateBp > 0 {
				c.Advice = fmt.Sprintf("Good. Any extra should go to %s (%.1f%% interest).", in.HighestRateLoan, float64(in.HighestRateBp)/100)
			}
		case t.Debt > 0:
			c.Status, c.Advice = Warn, "You're borrowing as fast as you repay. Freeze new loans."
		default:
			c.Status, c.Advice = Bad, "No repayments were recorded. Record each loan payment so your debt-free date stays accurate."
		}
		checks = append(checks, c)
	} else {
		checks = append(checks, Check{ID: "debt_direction", Title: "Debt is going down", Status: Good, Metric: "No open loans",
			Detail: "You have no debts recorded.", Formula: "debt repayments vs new borrowing over the window"})
	}

	// 4. Debt payments are manageable: monthly debt payments ÷ expected income.
	{
		income := in.ExpectedIncomeNow
		if income == 0 {
			income = t.Income / n
		}
		r := money.Ratio(in.MonthlyDebtPayments, income)
		c := Check{ID: "debt_burden", Title: "Debt payments are manageable", Value: r,
			Formula: "planned monthly debt payments ÷ expected monthly income; good < 30%, watch < 50%, fix ≥ 50%"}
		switch {
		case in.MonthlyDebtPayments == 0:
			c.Status, c.Metric, c.Detail = Good, "No debt payments", "None of your income goes to loan repayments."
		case r < 0.3:
			c.Status, c.Metric, c.Detail = Good, pct(r)+" of income", d(in.MonthlyDebtPayments)+" a month goes to loan repayments."
		default:
			c.Metric, c.Detail = pct(r)+" of income", d(in.MonthlyDebtPayments)+" a month goes to loan repayments."
			c.Advice = "Over 30% of income going to debt is heavy. Take no new loans and clear the smallest or most expensive loan first."
			if r < 0.5 {
				c.Status = Warn
			} else {
				c.Status = Bad
			}
		}
		checks = append(checks, c)
	}

	// 5. Savings rate: (saving + invest spending) ÷ income.
	{
		r := money.Ratio(t.Savings, t.Income)
		c := Check{ID: "savings_rate", Title: "Building wealth (saving or investing)", Value: r, Metric: pct(r) + " of income",
			Detail:  fmt.Sprintf("%s went to savings or investments over %s.", d(t.Savings), period),
			Formula: "money moved to savings/investment categories ÷ planned income; good ≥ 10%, watch > 0, fix = 0"}
		switch {
		case r >= 0.1:
			c.Status = Good
		case r > 0:
			c.Status = Warn
		default:
			c.Status = Bad
		}
		if r < 0.1 {
			c.Advice = fmt.Sprintf("Pay yourself first: move %s (10%%) to savings the day your salary arrives.", d(money.MulBp(t.Income/n, 1000)))
		}
		checks = append(checks, c)
	}

	// 6. Emergency fund coverage: balance ÷ average monthly essentials (needs + debt payments).
	{
		essentials := (t.Need + t.Debt) / n
		months := money.Ratio(in.EmergencyFundMinor, essentials)
		c := Check{ID: "emergency_fund", Title: "An emergency fund", Value: months,
			Formula: "emergency-fund balance ÷ average monthly essentials (needs + debt payments); good ≥ 3 months, watch ≥ 1, fix < 1"}
		switch {
		case in.EmergencyFundAccounts == 0:
			c.Status, c.Metric, c.Detail = Warn, "Not tracked", "Mark a savings account as your emergency fund to track it."
			c.Advice = "Start an emergency fund: one month of essential costs first, then three to six."
		case essentials == 0:
			c.Status, c.Metric, c.Detail = Good, d(in.EmergencyFundMinor)+" saved", "Record a few weeks of spending to see how many months it covers."
		default:
			c.Metric = fmt.Sprintf("%.1f months of essentials", months)
			c.Detail = fmt.Sprintf("%s covers about %.1f months of %s essential costs.", d(in.EmergencyFundMinor), months, d(essentials))
			switch {
			case months >= 3:
				c.Status = Good
			case months >= 1:
				c.Status, c.Advice = Warn, "Keep going until you have three months of essentials set aside."
			default:
				c.Status, c.Advice = Bad, fmt.Sprintf("Build one month of essentials (%s) first, so a surprise doesn't become a loan.", d(essentials))
			}
		}
		checks = append(checks, c)
	}

	// 7. Wants share: want spending ÷ total spending.
	{
		r := money.Ratio(t.Want, t.Spent)
		c := Check{ID: "wants", Title: "“Wants” under control", Value: r, Metric: pct(r) + " of spending",
			Detail:  fmt.Sprintf("%s went on eating out, fun, clothes and other wants.", d(t.Want)),
			Formula: "spending in “want” categories ÷ total spending; good ≤ 30%, watch ≤ 40%, fix > 40%"}
		switch {
		case r <= 0.3:
			c.Status = Good
		case r <= 0.4:
			c.Status = Warn
		default:
			c.Status = Bad
		}
		if r > 0.3 {
			c.Advice = fmt.Sprintf("Bringing wants down to 30%% frees about %s a month for debt or savings.", d(max(0, t.Want-money.MulBp(t.Spent, 3000))/n))
		}
		checks = append(checks, c)
	}

	// 8. Budgets respected in the current period.
	{
		c := Check{ID: "budgets", Title: "Keeping to your budgets", Value: float64(len(in.BudgetsOver)),
			Formula: "budgets over their limit in the current period; good = 0, watch ≤ 2, fix > 2"}
		switch {
		case in.BudgetsTotal == 0:
			c.Status, c.Metric, c.Detail = Warn, "No budgets set", "Without limits you can't be warned before you overspend."
			c.Advice = "Set budgets for food, transport and fun."
		case len(in.BudgetsOver) == 0:
			c.Status, c.Metric, c.Detail = Good, "All within limits", "Every budget is within its limit this period."
		default:
			c.Metric = fmt.Sprintf("%d over", len(in.BudgetsOver))
			c.Detail = "Over the limit: " + join(in.BudgetsOver) + "."
			c.Advice = fmt.Sprintf("Cut back on %s or set a realistic limit for it. A limit you always break isn't a plan.", in.BudgetsOver[0])
			if len(in.BudgetsOver) <= 2 {
				c.Status = Warn
			} else {
				c.Status = Bad
			}
		}
		checks = append(checks, c)
	}

	// 9. Next 6 months covered by expected income.
	if len(in.Upcoming) > 0 {
		var short, tight []UpcomingMonth
		for _, u := range in.Upcoming {
			left := u.IncomeMinor - u.PaymentsMinor
			if left < 0 {
				short = append(short, u)
			} else if u.IncomeMinor > 0 && left < money.MulBp(u.IncomeMinor, 1500) {
				tight = append(tight, u)
			}
		}
		c := Check{ID: "upcoming", Title: "The next 6 months are covered", Value: float64(len(short)),
			Formula: "for each of the next 6 months: expected income − planned payments; fix if negative, watch if under 15% of income"}
		switch {
		case len(short) > 0:
			first := short[0]
			ahead := calendar.MonthsBetween(calendar.MonthOf(in.Today), first.Month)
			c.Status, c.Metric = Bad, fmt.Sprintf("%d month(s) short", len(short))
			c.Detail = fmt.Sprintf("In %s planned payments exceed expected income by %s.", first.Month.Label(), d(first.PaymentsMinor-first.IncomeMinor))
			c.Advice = fmt.Sprintf("Start setting aside %s a month now to cover %s.", d((first.PaymentsMinor-first.IncomeMinor)/int64(max(1, ahead))), first.Month.Label())
		case len(tight) > 0:
			c.Status, c.Metric = Warn, fmt.Sprintf("%d tight month(s)", len(tight))
			c.Detail = "Less than 15% of income is left after payments in " + tight[0].Month.Label() + "."
		default:
			c.Status, c.Metric, c.Detail = Good, "All covered", "Expected income covers all planned payments in the coming months."
		}
		checks = append(checks, c)
	}

	// 10. On course to be debt-free.
	if in.DebtTotalMinor > 0 {
		c := Check{ID: "debt_free", Title: "On course to be debt-free", Value: float64(in.MonthsToDebtFree),
			Formula: "months until every loan clears at planned payments (interest on the reducing balance); good ≤ 24, watch > 24, fix = never"}
		switch {
		case in.MonthsToDebtFree < 0:
			c.Status, c.Metric = Bad, "Never at this rate"
			c.Detail = "At least one loan's payment doesn't cover its interest."
			c.Advice = "Raise the payment on that loan above its monthly interest, or it will keep growing."
		case in.MonthsToDebtFree <= 24:
			c.Status = Good
			c.Metric, c.Detail = calendar.MonthOf(in.Today).Add(in.MonthsToDebtFree).Label(), fmt.Sprintf("%d months to go.", in.MonthsToDebtFree)
		default:
			c.Status = Warn
			c.Metric, c.Detail = calendar.MonthOf(in.Today).Add(in.MonthsToDebtFree).Label(), fmt.Sprintf("%d months to go.", in.MonthsToDebtFree)
			c.Advice = "Paying a little extra on the highest-interest loan shortens this the most."
		}
		checks = append(checks, c)
	}

	// 11. Goals on track.
	if in.GoalsTotal > 0 {
		c := Check{ID: "goals", Title: "Goals on track", Value: float64(len(in.GoalsBehind)),
			Formula: "goals behind a straight line from start to target date; good = 0 behind"}
		if len(in.GoalsBehind) == 0 {
			c.Status, c.Metric, c.Detail = Good, "All on track", "Every goal with a deadline is on or ahead of schedule."
		} else {
			c.Status, c.Metric = Warn, fmt.Sprintf("%d behind", len(in.GoalsBehind))
			c.Detail = "Behind schedule: " + join(in.GoalsBehind) + "."
			c.Advice = "Raise the monthly contribution or move the target date so the plan stays realistic."
		}
		checks = append(checks, c)
	}

	// 12. Consistency: are expenses being recorded?
	{
		c := Check{ID: "consistency", Title: "Recording every expense", Value: float64(in.EntriesLast14Days),
			Metric:  fmt.Sprintf("%d entries in 14 days", in.EntriesLast14Days),
			Detail:  "The numbers above are only as good as what's recorded.",
			Formula: "expense entries in the last 14 days; good ≥ 8, watch ≥ 3, fix < 3"}
		switch {
		case in.EntriesLast14Days >= 8:
			c.Status = Good
		case in.EntriesLast14Days >= 3:
			c.Status = Warn
		default:
			c.Status = Bad
		}
		if c.Status != Good {
			c.Advice = "Record every expense the same day, even small ones."
		}
		checks = append(checks, c)
	}

	order := map[Status]int{Bad: 0, Warn: 1, Good: 2}
	sort.SliceStable(checks, func(i, j int) bool { return order[checks[i].Status] < order[checks[j].Status] })
	var s Summary
	actions := []Check{}
	for _, c := range checks {
		switch c.Status {
		case Good:
			s.Good++
		case Warn:
			s.Warn++
		case Bad:
			s.Bad++
		}
		if c.Status != Good && c.Advice != "" && len(actions) < 3 {
			actions = append(actions, c)
		}
	}
	verdict := "Mostly on track, with a few things to fix."
	switch {
	case s.Bad == 0 && s.Warn <= 1:
		verdict = "You're heading in the right direction."
	case s.Bad >= 3:
		verdict = "Your money is moving the wrong way. Start with the actions below."
	}
	return Result{Period: period, Checks: checks, Summary: s, Actions: actions, Verdict: verdict}
}

// Direction compares average monthly cash flow in the older and newer halves of a series.
// Returns the change per month (positive = keeping more) and whether there was enough data.
func Direction(series []MonthData) (changeMinor int64, ok bool) {
	var with []MonthData
	for _, m := range series {
		if m.HasData() {
			with = append(with, m)
		}
	}
	if len(with) < 2 {
		return 0, false
	}
	half := len(with) / 2
	avg := func(xs []MonthData) int64 {
		var s int64
		for _, x := range xs {
			s += x.CashFlowMinor()
		}
		return s / int64(len(xs))
	}
	return avg(with[half:]) - avg(with[:half]), true
}

// ratioOrOverspend returns spent ÷ income, or 2 (clearly over) when there is spending but no income.
func ratioOrOverspend(spent, income int64) float64 {
	if income == 0 {
		if spent > 0 {
			return 2
		}
		return 0
	}
	return money.Ratio(spent, income)
}

func join(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

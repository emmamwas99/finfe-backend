package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"finfe/internal/domain/calendar"
	"finfe/internal/domain/debt"
	"finfe/internal/domain/financialhealth"
	goaldomain "finfe/internal/domain/goal"
)

func (a *API) monthRange(r *http.Request) (calendar.Month, error) {
	if s := r.URL.Query().Get("month"); s != "" {
		return calendar.ParseMonth(s)
	}
	var tz string
	if err := a.db.QueryRow(r.Context(), `SELECT timezone FROM users WHERE id=$1`, principal(r).UserID).Scan(&tz); err != nil {
		return calendar.Month{}, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return calendar.Month{}, err
	}
	return calendar.MonthOf(time.Now().In(loc)), nil
}
func (a *API) monthError(w http.ResponseWriter, r *http.Request, err error) {
	if r.URL.Query().Get("month") != "" {
		badJSON(w, err)
	} else {
		serverError(a, w, err)
	}
}
func (a *API) expenseSummary(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from, to := m.Range()
	rows, err := a.db.Query(r.Context(), `SELECT currency,COALESCE(sum(amount_minor),0)::bigint AS total_minor,count(*)::bigint AS count FROM expenses WHERE user_id=$1 AND occurred_on >= $2 AND occurred_on < $3 GROUP BY currency ORDER BY currency`, principal(r).UserID, from, to)
	if err != nil {
		serverError(a, w, err)
		return
	}
	data, err := collect(rows)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"month": m.String(), "data": data})
}
func (a *API) spendingBreakdown(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from, to := m.Range()
	rows, err := a.db.Query(r.Context(), `SELECT e.currency,c.id AS category_id,c.name,c.kind,sum(e.amount_minor)::bigint AS total_minor,count(*)::bigint AS count FROM expenses e JOIN categories c ON c.id=e.category_id AND c.user_id=e.user_id WHERE e.user_id=$1 AND e.occurred_on >= $2 AND e.occurred_on < $3 GROUP BY e.currency,c.id,c.name,c.kind ORDER BY total_minor DESC`, principal(r).UserID, from, to)
	if err != nil {
		serverError(a, w, err)
		return
	}
	data, err := collect(rows)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"month": m.String(), "data": data})
}
func (a *API) monthlyReport(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from, to := m.Range()
	rows, err := a.db.Query(r.Context(), `WITH totals AS (
      SELECT currency, sum(amount_minor)::bigint AS expenses_minor,0::bigint AS income_minor,0::bigint AS borrowed_minor FROM expenses WHERE user_id=$1 AND occurred_on >= $2 AND occurred_on < $3 GROUP BY currency
      UNION ALL
      SELECT currency,0,sum(amount_minor) FILTER (WHERE NOT is_borrowed)::bigint,sum(amount_minor) FILTER (WHERE is_borrowed)::bigint FROM income WHERE user_id=$1 AND occurred_on >= $2 AND occurred_on < $3 GROUP BY currency
    ) SELECT currency,coalesce(sum(income_minor),0)::bigint AS income_minor,coalesce(sum(borrowed_minor),0)::bigint AS borrowed_minor,coalesce(sum(expenses_minor),0)::bigint AS expenses_minor,(coalesce(sum(income_minor),0)-coalesce(sum(expenses_minor),0))::bigint AS cash_flow_minor FROM totals GROUP BY currency ORDER BY currency`, principal(r).UserID, from, to)
	if err != nil {
		serverError(a, w, err)
		return
	}
	data, err := collect(rows)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"month": m.String(), "data": data})
}
func (a *API) annualReport(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	year := m.Time().Year()
	if s := r.URL.Query().Get("year"); s != "" {
		if _, err := time.Parse("2006", s); err != nil {
			badJSON(w, err)
			return
		}
		year, _ = strconv.Atoi(s)
	}
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(1, 0, 0)
	a.monthSeries(w, r, from, to)
}
func (a *API) cashFlow(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	a.monthSeries(w, r, m.Add(-11).Time(), m.Next().Time())
}
func (a *API) monthSeries(w http.ResponseWriter, r *http.Request, from, to time.Time) {
	rows, err := a.db.Query(r.Context(), `WITH x AS (
      SELECT date_trunc('month',occurred_on)::date AS month,currency,0::bigint AS earned_minor,0::bigint AS borrowed_minor,sum(amount_minor)::bigint AS spent_minor FROM expenses WHERE user_id=$1 AND occurred_on >= $2 AND occurred_on < $3 GROUP BY 1,2
      UNION ALL
      SELECT date_trunc('month',occurred_on)::date,currency,coalesce(sum(amount_minor) FILTER (WHERE NOT is_borrowed),0)::bigint,coalesce(sum(amount_minor) FILTER (WHERE is_borrowed),0)::bigint,0::bigint FROM income WHERE user_id=$1 AND occurred_on >= $2 AND occurred_on < $3 GROUP BY 1,2
    ) SELECT month,currency,sum(earned_minor)::bigint AS earned_minor,sum(borrowed_minor)::bigint AS borrowed_minor,sum(spent_minor)::bigint AS spent_minor,(sum(earned_minor)-sum(spent_minor))::bigint AS cash_flow_minor FROM x GROUP BY month,currency ORDER BY month,currency`, principal(r).UserID, from, to)
	if err != nil {
		serverError(a, w, err)
		return
	}
	data, err := collect(rows)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"data": data})
}

func (a *API) budgetProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	var amount int64
	var currency, period string
	err = a.db.QueryRow(r.Context(), `SELECT amount_minor,currency,period FROM budgets WHERE id=$1 AND user_id=$2`, id, principal(r).UserID).Scan(&amount, &currency, &period)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Budget not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	from, to := m.Range()
	if period == "weekly" {
		var tz string
		if err := a.db.QueryRow(r.Context(), `SELECT timezone FROM users WHERE id=$1`, principal(r).UserID).Scan(&tz); err != nil {
			serverError(a, w, err)
			return
		}
		today := calendar.Today(time.Now(), tz)
		from = today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
		to = from.AddDate(0, 0, 7)
	}
	var spent int64
	err = a.db.QueryRow(r.Context(), `SELECT coalesce(sum(e.amount_minor),0)::bigint FROM expenses e WHERE e.user_id=$1 AND e.currency=$2 AND e.occurred_on >= $3 AND e.occurred_on < $4 AND EXISTS (SELECT 1 FROM budget_categories bc WHERE bc.budget_id=$5 AND bc.category_id=e.category_id AND bc.user_id=$1)`, principal(r).UserID, currency, from, to, id).Scan(&spent)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"budget_id": id, "currency": currency, "amount_minor": amount, "spent_minor": spent, "remaining_minor": amount - spent, "from": from.Format("2006-01-02"), "to": to.Format("2006-01-02")})
}

func (a *API) savingsTransactions(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	_, err := one(r.Context(), a.db, `SELECT id FROM savings_accounts WHERE id=$1 AND user_id=$2`, id, principal(r).UserID)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Savings account not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT * FROM savings_transactions WHERE account_id=$1 AND user_id=$2 ORDER BY occurred_on DESC,created_at DESC LIMIT 100`, id, principal(r).UserID)
	if err != nil {
		serverError(a, w, err)
		return
	}
	data, err := collect(rows)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"data": data})
}
func (a *API) addSavingsTransaction(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	var v struct {
		Kind        string `json:"kind"`
		AmountMinor int64  `json:"amount_minor"`
		OccurredOn  string `json:"occurred_on"`
		Note        string `json:"note"`
	}
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	if v.AmountMinor <= 0 || (v.Kind != "deposit" && v.Kind != "withdrawal") || len(v.Note) > 500 {
		fail(w, 400, "validation_failed", "Invalid savings transaction")
		return
	}
	date, err := parseDate(v.OccurredOn)
	if err != nil {
		badJSON(w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(a, w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var archived bool
	err = tx.QueryRow(r.Context(), `SELECT archived FROM savings_accounts WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, principal(r).UserID).Scan(&archived)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Savings account not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	if archived {
		fail(w, 409, "archived", "Savings account is archived")
		return
	}
	if v.Kind == "withdrawal" {
		var balance int64
		err = tx.QueryRow(r.Context(), `SELECT coalesce(sum(CASE WHEN kind='deposit' THEN amount_minor ELSE -amount_minor END),0)::bigint FROM savings_transactions WHERE account_id=$1 AND user_id=$2`, id, principal(r).UserID).Scan(&balance)
		if err != nil {
			serverError(a, w, err)
			return
		}
		if v.AmountMinor > balance {
			fail(w, 409, "insufficient_funds", "Withdrawal exceeds balance")
			return
		}
	}
	rows, err := tx.Query(r.Context(), `INSERT INTO savings_transactions(id,user_id,account_id,kind,amount_minor,occurred_on,note) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING *`, uuid.New(), principal(r).UserID, id, v.Kind, v.AmountMinor, date, v.Note)
	if err != nil {
		dbFailure(a, w, err)
		return
	}
	data, err := collect(rows)
	if err != nil {
		serverError(a, w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 201, map[string]any{"data": data[0]})
}
func (a *API) goalProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	var target, starting, contributed int64
	var currency string
	err := a.db.QueryRow(r.Context(), `SELECT target_minor,starting_minor,currency FROM goals WHERE id=$1 AND user_id=$2`, id, principal(r).UserID).Scan(&target, &starting, &currency)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Goal not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	err = a.db.QueryRow(r.Context(), `SELECT coalesce(sum(amount_minor),0)::bigint FROM expenses WHERE goal_id=$1 AND user_id=$2 AND currency=$3`, id, principal(r).UserID, currency).Scan(&contributed)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"goal_id": id, "currency": currency, "target_minor": target, "saved_minor": starting + contributed, "remaining_minor": max(0, target-starting-contributed), "completed": starting+contributed >= target})
}
func (a *API) goalContribution(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	var v struct {
		AmountMinor int64     `json:"amount_minor"`
		CategoryID  uuid.UUID `json:"category_id"`
		OccurredOn  string    `json:"occurred_on"`
		Note        string    `json:"note"`
	}
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	date, err := parseDate(v.OccurredOn)
	if err != nil || v.AmountMinor <= 0 || v.CategoryID == uuid.Nil {
		fail(w, 400, "validation_failed", "Invalid contribution")
		return
	}
	var currency string
	err = a.db.QueryRow(r.Context(), `SELECT currency FROM goals WHERE id=$1 AND user_id=$2`, id, principal(r).UserID).Scan(&currency)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Goal not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	row, err := one(r.Context(), a.db, `INSERT INTO expenses(id,user_id,occurred_on,amount_minor,currency,category_id,note,goal_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *`, uuid.New(), principal(r).UserID, date, v.AmountMinor, currency, v.CategoryID, v.Note, id)
	if err != nil {
		dbFailure(a, w, err)
		return
	}
	respond(w, 201, map[string]any{"data": row})
}

func (a *API) financialHealth(w http.ResponseWriter, r *http.Request) {
	u, err := a.q.GetUserByID(r.Context(), principal(r).UserID)
	if err != nil {
		serverError(a, w, err)
		return
	}
	tz := u.Timezone
	today := calendar.Today(time.Now(), tz)
	current := calendar.MonthOf(today)
	window := make([]financialhealth.MonthData, 3)
	for i := range window {
		window[i] = financialhealth.MonthData{Month: current.Add(i - 2), IncomeMinor: u.ExpectedIncomeMinor, ByKind: map[string]int64{}}
	}
	expectations, err := a.q.ListIncomeExpectations(r.Context(), u.ID)
	if err != nil {
		serverError(a, w, err)
		return
	}
	for i := range window {
		month := window[i].Month.Time()
		for _, expectation := range expectations {
			switch expectation.Scope {
			case "from":
				if !expectation.Month.After(month) {
					window[i].IncomeMinor = expectation.AmountMinor
				}
			case "only":
				if expectation.Month.Equal(month) {
					window[i].IncomeMinor = expectation.AmountMinor
				}
			}
		}
	}
	currentExpectedIncome := window[2].IncomeMinor
	from := window[0].Month.Time()
	to := current.Next().Time()
	rows, err := a.db.Query(r.Context(), `SELECT date_trunc('month',e.occurred_on)::date AS month,c.kind,sum(e.amount_minor)::bigint,count(*)::bigint FROM expenses e JOIN categories c ON c.id=e.category_id WHERE e.user_id=$1 AND e.currency=$2 AND e.occurred_on >= $3 AND e.occurred_on < $4 GROUP BY 1,2`, u.ID, u.Currency, from, to)
	if err != nil {
		serverError(a, w, err)
		return
	}
	for rows.Next() {
		var d time.Time
		var kind string
		var total, count int64
		if err = rows.Scan(&d, &kind, &total, &count); err != nil {
			rows.Close()
			serverError(a, w, err)
			return
		}
		i := calendar.MonthsBetween(window[0].Month, calendar.MonthOf(d))
		if i >= 0 && i < 3 {
			window[i].SpentMinor += total
			window[i].ByKind[kind] += total
			window[i].Entries += count
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		serverError(a, w, err)
		return
	}
	rows, err = a.db.Query(r.Context(), `SELECT date_trunc('month',occurred_on)::date AS month,coalesce(sum(amount_minor) FILTER (WHERE NOT is_borrowed),0)::bigint,coalesce(sum(amount_minor) FILTER (WHERE is_borrowed),0)::bigint FROM income WHERE user_id=$1 AND currency=$2 AND occurred_on >= $3 AND occurred_on < $4 GROUP BY 1`, u.ID, u.Currency, from, to)
	if err != nil {
		serverError(a, w, err)
		return
	}
	for rows.Next() {
		var d time.Time
		var earned, borrowed int64
		if err = rows.Scan(&d, &earned, &borrowed); err != nil {
			rows.Close()
			serverError(a, w, err)
			return
		}
		i := calendar.MonthsBetween(window[0].Month, calendar.MonthOf(d))
		if i >= 0 && i < 3 {
			window[i].EarnedMinor = earned
			window[i].BorrowedMinor = borrowed
			window[i].IncomeMinor = earned
			window[i].Entries++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		serverError(a, w, err)
		return
	}
	in := financialhealth.Input{Currency: u.Currency, Today: today, Window: window, ExpectedIncomeNow: currentExpectedIncome}
	err = a.db.QueryRow(r.Context(), `SELECT coalesce(sum(CASE WHEN t.kind='deposit' THEN t.amount_minor WHEN t.kind='withdrawal' THEN -t.amount_minor ELSE 0 END),0)::bigint,count(DISTINCT a.id)::bigint FROM savings_accounts a LEFT JOIN savings_transactions t ON t.account_id=a.id WHERE a.user_id=$1 AND a.currency=$2 AND a.is_emergency_fund AND NOT a.archived`, u.ID, u.Currency).Scan(&in.EmergencyFundMinor, &in.EmergencyFundAccounts)
	if err != nil {
		serverError(a, w, err)
		return
	}
	var entries int64
	err = a.db.QueryRow(r.Context(), `SELECT count(*)::bigint FROM expenses WHERE user_id=$1 AND currency=$2 AND occurred_on >= $3`, u.ID, u.Currency, today.AddDate(0, 0, -14)).Scan(&entries)
	if err != nil {
		serverError(a, w, err)
		return
	}
	in.EntriesLast14Days = entries
	rows, err = a.db.Query(r.Context(), `SELECT l.name,l.principal_minor,l.monthly_payment_minor,l.interest_rate_bp,coalesce((SELECT sum(e.amount_minor) FROM expenses e WHERE e.loan_id=l.id AND e.user_id=l.user_id AND e.currency=l.currency),0)::bigint FROM loans l WHERE l.user_id=$1 AND l.currency=$2 AND NOT l.closed`, u.ID, u.Currency)
	if err != nil {
		serverError(a, w, err)
		return
	}
	var debtLoans []debt.Loan
	for rows.Next() {
		var name string
		var principal, payment, paid int64
		var rate int32
		if err = rows.Scan(&name, &principal, &payment, &rate, &paid); err != nil {
			rows.Close()
			serverError(a, w, err)
			return
		}
		balance := max(0, principal-paid)
		if balance == 0 {
			continue
		}
		in.OpenLoans++
		in.DebtTotalMinor += balance
		in.MonthlyDebtPayments += payment
		if int64(rate) > in.HighestRateBp {
			in.HighestRateBp = int64(rate)
			in.HighestRateLoan = name
		}
		debtLoans = append(debtLoans, debt.Loan{BalanceMinor: balance, PaymentMinor: payment, RateBp: int64(rate)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		serverError(a, w, err)
		return
	}
	if len(debtLoans) > 0 {
		in.MonthsToDebtFree = debt.Simulate(debtLoans, debt.Options{MaxMonths: 1200}).Months
	}
	monthFrom, monthTo := current.Range()
	weekFrom := today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
	weekTo := weekFrom.AddDate(0, 0, 7)
	rows, err = a.db.Query(r.Context(), `SELECT b.name,b.amount_minor,coalesce((SELECT sum(e.amount_minor) FROM budget_categories bc JOIN expenses e ON e.category_id=bc.category_id AND e.user_id=bc.user_id WHERE bc.budget_id=b.id AND bc.user_id=b.user_id AND e.currency=b.currency AND e.occurred_on >= CASE WHEN b.period='weekly' THEN $3::date ELSE $4::date END AND e.occurred_on < CASE WHEN b.period='weekly' THEN $5::date ELSE $6::date END),0)::bigint FROM budgets b WHERE b.user_id=$1 AND b.currency=$2`, u.ID, u.Currency, weekFrom, monthFrom, weekTo, monthTo)
	if err != nil {
		serverError(a, w, err)
		return
	}
	for rows.Next() {
		var name string
		var limit, spent int64
		if err = rows.Scan(&name, &limit, &spent); err != nil {
			rows.Close()
			serverError(a, w, err)
			return
		}
		in.BudgetsTotal++
		if spent > limit {
			in.BudgetsOver = append(in.BudgetsOver, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		serverError(a, w, err)
		return
	}
	rows, err = a.db.Query(r.Context(), `SELECT g.name,g.target_minor,g.starting_minor,g.target_month,g.created_at,coalesce((SELECT sum(e.amount_minor) FROM expenses e WHERE e.goal_id=g.id AND e.user_id=g.user_id AND e.currency=g.currency),0)::bigint FROM goals g WHERE g.user_id=$1 AND g.currency=$2`, u.ID, u.Currency)
	if err != nil {
		serverError(a, w, err)
		return
	}
	for rows.Next() {
		var name string
		var target, starting, contributed int64
		var targetDate *time.Time
		var created time.Time
		if err = rows.Scan(&name, &target, &starting, &targetDate, &created, &contributed); err != nil {
			rows.Close()
			serverError(a, w, err)
			return
		}
		in.GoalsTotal++
		var targetMonth *calendar.Month
		if targetDate != nil {
			m := calendar.MonthOf(*targetDate)
			targetMonth = &m
		}
		status := goaldomain.Compute(starting+contributed, target, created, targetMonth, today).Status
		if status == goaldomain.Behind || status == goaldomain.Overdue {
			in.GoalsBehind = append(in.GoalsBehind, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		serverError(a, w, err)
		return
	}
	result := financialhealth.Evaluate(in)
	respond(w, 200, result)
}

package httpapi

import (
	"net/http"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"finfe/internal/domain/calendar"
	"finfe/internal/domain/debt"
	"finfe/internal/domain/financialhealth"
	goaldomain "finfe/internal/domain/goal"
)

func (a *MongoAPI) savingsTrends(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from, to := m.Add(-11).Time(), m.Next().Time()
	owner := principal(r).UserID.String()
	accounts, err := a.docs(r.Context(), "savings_accounts", bson.M{"user_id": owner})
	if err != nil {
		a.serverError(w, err)
		return
	}
	currencies := map[string]string{}
	for _, account := range accounts {
		currencies[stringField(account, "id")] = stringField(account, "currency")
	}
	txs, err := a.docs(r.Context(), "savings_transactions", bson.M{"user_id": owner, "occurred_on": bson.M{"$gte": from.Format("2006-01-02"), "$lt": to.Format("2006-01-02")}})
	if err != nil {
		a.serverError(w, err)
		return
	}
	group := map[string]bson.M{}
	for _, tx := range txs {
		currency := currencies[stringField(tx, "account_id")]
		date := stringField(tx, "occurred_on")
		if currency == "" || len(date) < 7 {
			continue
		}
		month := monthDate(date)
		key := month.Format("2006-01-02") + ":" + currency
		row := group[key]
		if row == nil {
			row = bson.M{"month": month, "currency": currency, "deposits_minor": int64(0), "withdrawals_minor": int64(0)}
			group[key] = row
		}
		field := "deposits_minor"
		if stringField(tx, "kind") == "withdrawal" {
			field = "withdrawals_minor"
		}
		row[field] = row[field].(int64) + int64Field(tx, "amount_minor")
	}
	keys := make([]string, 0, len(group))
	for k := range group {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data := make([]bson.M, 0, len(keys))
	for _, k := range keys {
		data = append(data, group[k])
	}
	respond(w, 200, map[string]any{"data": data})
}

func (a *MongoAPI) healthTrends(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from, to := m.Add(-11).Time(), m.Next().Time()
	owner := principal(r).UserID.String()
	filter := bson.M{"user_id": owner, "occurred_on": bson.M{"$gte": from.Format("2006-01-02"), "$lt": to.Format("2006-01-02")}}
	expenses, err := a.docs(r.Context(), "expenses", filter)
	if err != nil {
		a.serverError(w, err)
		return
	}
	incomes, err := a.docs(r.Context(), "income", filter)
	if err != nil {
		a.serverError(w, err)
		return
	}
	categories, err := a.docs(r.Context(), "categories", bson.M{"user_id": owner})
	if err != nil {
		a.serverError(w, err)
		return
	}
	kinds := map[string]string{}
	for _, c := range categories {
		kinds[stringField(c, "id")] = stringField(c, "kind")
	}
	group := map[string]bson.M{}
	get := func(doc bson.M) bson.M {
		date := stringField(doc, "occurred_on")
		if len(date) < 7 {
			return nil
		}
		month := monthDate(date)
		currency := stringField(doc, "currency")
		key := month.Format("2006-01-02") + ":" + currency
		row := group[key]
		if row == nil {
			row = bson.M{"month": month, "currency": currency, "earned_minor": int64(0), "spent_minor": int64(0), "saving_minor": int64(0)}
			group[key] = row
		}
		return row
	}
	for _, e := range expenses {
		row := get(e)
		if row == nil {
			continue
		}
		amount := int64Field(e, "amount_minor")
		row["spent_minor"] = row["spent_minor"].(int64) + amount
		kind := kinds[stringField(e, "category_id")]
		if kind == "saving" || kind == "invest" {
			row["saving_minor"] = row["saving_minor"].(int64) + amount
		}
	}
	for _, i := range incomes {
		if boolField(i, "is_borrowed") {
			continue
		}
		row := get(i)
		if row != nil {
			row["earned_minor"] = row["earned_minor"].(int64) + int64Field(i, "amount_minor")
		}
	}
	keys := make([]string, 0, len(group))
	for k := range group {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data := make([]bson.M, 0, len(keys))
	for _, k := range keys {
		row := group[k]
		earned, spent, saved := int64Field(row, "earned_minor"), int64Field(row, "spent_minor"), int64Field(row, "saving_minor")
		row["cash_flow_minor"] = earned - spent
		if earned > 0 {
			row["savings_rate_bp"] = saved * 10000 / earned
		} else {
			row["savings_rate_bp"] = nil
		}
		data = append(data, row)
	}
	respond(w, 200, map[string]any{"data": data})
}

func (a *MongoAPI) financialHealth(w http.ResponseWriter, r *http.Request) {
	u, err := a.authStore.GetUserByID(r.Context(), principal(r).UserID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	today := calendar.Today(time.Now(), u.Timezone)
	current := calendar.MonthOf(today)
	owner := u.ID.String()
	window := make([]financialhealth.MonthData, 3)
	for i := range window {
		window[i] = financialhealth.MonthData{Month: current.Add(i - 2), IncomeMinor: u.ExpectedIncomeMinor, ByKind: map[string]int64{}}
	}
	expectations, err := a.authStore.ListIncomeExpectations(r.Context(), u.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	for i := range window {
		month := window[i].Month.Time()
		for _, e := range expectations {
			switch e.Scope {
			case "from":
				if !e.Month.After(month) {
					window[i].IncomeMinor = e.AmountMinor
				}
			case "only":
				if e.Month.Equal(month) {
					window[i].IncomeMinor = e.AmountMinor
				}
			}
		}
	}
	currentExpected := window[2].IncomeMinor
	from, to := window[0].Month.Time(), current.Next().Time()
	filter := bson.M{"user_id": owner, "currency": u.Currency, "occurred_on": bson.M{"$gte": from.Format("2006-01-02"), "$lt": to.Format("2006-01-02")}}
	expenses, err := a.docs(r.Context(), "expenses", filter)
	if err != nil {
		a.serverError(w, err)
		return
	}
	incomes, err := a.docs(r.Context(), "income", filter)
	if err != nil {
		a.serverError(w, err)
		return
	}
	categories, err := a.docs(r.Context(), "categories", bson.M{"user_id": owner})
	if err != nil {
		a.serverError(w, err)
		return
	}
	kinds := map[string]string{}
	for _, c := range categories {
		kinds[stringField(c, "id")] = stringField(c, "kind")
	}
	for _, e := range expenses {
		date := stringField(e, "occurred_on")
		if len(date) < 7 {
			continue
		}
		i := calendar.MonthsBetween(window[0].Month, calendar.MonthOf(monthDate(date)))
		if i >= 0 && i < 3 {
			amount := int64Field(e, "amount_minor")
			window[i].SpentMinor += amount
			window[i].ByKind[kinds[stringField(e, "category_id")]] += amount
			window[i].Entries++
		}
	}
	monthIncome := map[int]bool{}
	for _, income := range incomes {
		date := stringField(income, "occurred_on")
		if len(date) < 7 {
			continue
		}
		i := calendar.MonthsBetween(window[0].Month, calendar.MonthOf(monthDate(date)))
		if i >= 0 && i < 3 {
			if boolField(income, "is_borrowed") {
				window[i].BorrowedMinor += int64Field(income, "amount_minor")
			} else {
				window[i].EarnedMinor += int64Field(income, "amount_minor")
			}
			monthIncome[i] = true
		}
	}
	for i := range monthIncome {
		window[i].IncomeMinor = window[i].EarnedMinor
		window[i].Entries++
	}
	in := financialhealth.Input{Currency: u.Currency, Today: today, Window: window, ExpectedIncomeNow: currentExpected}
	accounts, err := a.docs(r.Context(), "savings_accounts", bson.M{"user_id": owner, "currency": u.Currency, "is_emergency_fund": true, "archived": false})
	if err != nil {
		a.serverError(w, err)
		return
	}
	for _, account := range accounts {
		deposits, withdrawals, err := a.savingsTotals(r.Context(), owner, stringField(account, "id"))
		if err != nil {
			a.serverError(w, err)
			return
		}
		in.EmergencyFundMinor += deposits - withdrawals
		in.EmergencyFundAccounts++
	}
	for _, e := range expenses {
		if stringField(e, "occurred_on") >= today.AddDate(0, 0, -14).Format("2006-01-02") {
			in.EntriesLast14Days++
		}
	}
	loans, err := a.docs(r.Context(), "loans", bson.M{"user_id": owner, "currency": u.Currency, "closed": false})
	if err != nil {
		a.serverError(w, err)
		return
	}
	debtLoans := []debt.Loan{}
	for _, loan := range loans {
		paid := int64(0)
		payments, err := a.docs(r.Context(), "expenses", bson.M{"user_id": owner, "currency": u.Currency, "loan_id": stringField(loan, "id")})
		if err != nil {
			a.serverError(w, err)
			return
		}
		for _, e := range payments { paid += int64Field(e, "amount_minor") }
		balance := max(0, int64Field(loan, "principal_minor")-paid)
		if balance == 0 {
			continue
		}
		in.OpenLoans++
		in.DebtTotalMinor += balance
		payment := int64Field(loan, "monthly_payment_minor")
		in.MonthlyDebtPayments += payment
		rate := int64Field(loan, "interest_rate_bp")
		if rate > in.HighestRateBp {
			in.HighestRateBp = rate
			in.HighestRateLoan = stringField(loan, "name")
		}
		debtLoans = append(debtLoans, debt.Loan{BalanceMinor: balance, PaymentMinor: payment, RateBp: rate})
	}
	if len(debtLoans) > 0 {
		in.MonthsToDebtFree = debt.Simulate(debtLoans, debt.Options{MaxMonths: 1200}).Months
	}
	monthFrom, monthTo := current.Range()
	weekFrom := today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
	weekTo := weekFrom.AddDate(0, 0, 7)
	budgets, err := a.docs(r.Context(), "budgets", bson.M{"user_id": owner, "currency": u.Currency})
	if err != nil {
		a.serverError(w, err)
		return
	}
	links, err := a.docs(r.Context(), "budget_categories", bson.M{"user_id": owner})
	if err != nil {
		a.serverError(w, err)
		return
	}
	for _, budget := range budgets {
		in.BudgetsTotal++
		cats := map[string]bool{}
		for _, link := range links {
			if stringField(link, "budget_id") == stringField(budget, "id") {
				cats[stringField(link, "category_id")] = true
			}
		}
		start, end := monthFrom, monthTo
		if stringField(budget, "period") == "weekly" {
			start, end = weekFrom, weekTo
		}
		spent := int64(0)
		rows, err := a.docs(r.Context(), "expenses", bson.M{"user_id": owner, "currency": u.Currency, "occurred_on": bson.M{"$gte": start.Format("2006-01-02"), "$lt": end.Format("2006-01-02")}})
		if err != nil {
			a.serverError(w, err)
			return
		}
		for _, e := range rows {
			if cats[stringField(e, "category_id")] {
				spent += int64Field(e, "amount_minor")
			}
		}
		if spent > int64Field(budget, "amount_minor") {
			in.BudgetsOver = append(in.BudgetsOver, stringField(budget, "name"))
		}
	}
	goals, err := a.docs(r.Context(), "goals", bson.M{"user_id": owner, "currency": u.Currency})
	if err != nil {
		a.serverError(w, err)
		return
	}
	for _, goal := range goals {
		in.GoalsTotal++
		rows, err := a.docs(r.Context(), "expenses", bson.M{"user_id": owner, "currency": u.Currency, "goal_id": stringField(goal, "id")})
		if err != nil {
			a.serverError(w, err)
			return
		}
		contributed := int64(0)
		for _, e := range rows {
			contributed += int64Field(e, "amount_minor")
		}
		var targetMonth *calendar.Month
		if raw := stringField(goal, "target_month"); len(raw) >= 7 {
			parsed, err := calendar.ParseMonth(raw[:7])
			if err == nil {
				targetMonth = &parsed
			}
		}
		created, _ := goal["created_at"].(time.Time)
		status := goaldomain.Compute(int64Field(goal, "starting_minor")+contributed, int64Field(goal, "target_minor"), created, targetMonth, today).Status
		if status == goaldomain.Behind || status == goaldomain.Overdue {
			in.GoalsBehind = append(in.GoalsBehind, stringField(goal, "name"))
		}
	}
	respond(w, 200, financialhealth.Evaluate(in))
}

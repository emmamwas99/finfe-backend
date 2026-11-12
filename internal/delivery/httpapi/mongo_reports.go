package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"

	"finfe/internal/domain/calendar"
	"finfe/internal/infrastructure/security"
)

func (a *MongoAPI) docs(ctx context.Context, collection string, filter bson.M) ([]bson.M, error) {
	cur, err := a.database.DB.Collection(collection).Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := make([]bson.M, 0)
	for cur.Next(ctx) {
		var item bson.M
		if err := cur.Decode(&item); err != nil {
			return nil, err
		}
		delete(item, "_id")
		out = append(out, item)
	}
	return out, cur.Err()
}

func int64Field(doc bson.M, field string) int64 {
	switch x := doc[field].(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	default:
		return 0
	}
}
func stringField(doc bson.M, field string) string { x, _ := doc[field].(string); return x }
func boolField(doc bson.M, field string) bool     { x, _ := doc[field].(bool); return x }
func monthDate(s string) time.Time                { t, _ := time.Parse("2006-01-02", s[:7]+"-01"); return t }

func (a *MongoAPI) monthRange(r *http.Request) (calendar.Month, error) {
	if s := r.URL.Query().Get("month"); s != "" {
		return calendar.ParseMonth(s)
	}
	u, err := a.authStore.GetUserByID(r.Context(), principal(r).UserID)
	if err != nil {
		return calendar.Month{}, err
	}
	loc, err := time.LoadLocation(u.Timezone)
	if err != nil {
		return calendar.Month{}, err
	}
	return calendar.MonthOf(time.Now().In(loc)), nil
}

func (a *MongoAPI) monthError(w http.ResponseWriter, r *http.Request, err error) {
	if r.URL.Query().Get("month") != "" {
		badJSON(w, err)
	} else {
		a.serverError(w, err)
	}
}

func (a *MongoAPI) expenseSummary(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from, to := m.Range()
	expenses, err := a.docs(r.Context(), "expenses", bson.M{"user_id": principal(r).UserID.String(), "occurred_on": bson.M{"$gte": from.Format("2006-01-02"), "$lt": to.Format("2006-01-02")}})
	if err != nil {
		a.serverError(w, err)
		return
	}
	group := map[string]bson.M{}
	for _, e := range expenses {
		c := stringField(e, "currency")
		row := group[c]
		if row == nil {
			row = bson.M{"currency": c, "total_minor": int64(0), "count": int64(0)}
			group[c] = row
		}
		row["total_minor"] = row["total_minor"].(int64) + int64Field(e, "amount_minor")
		row["count"] = row["count"].(int64) + 1
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
	respond(w, 200, map[string]any{"month": m.String(), "data": data})
}

func (a *MongoAPI) spendingBreakdown(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from, to := m.Range()
	owner := principal(r).UserID.String()
	expenses, err := a.docs(r.Context(), "expenses", bson.M{"user_id": owner, "occurred_on": bson.M{"$gte": from.Format("2006-01-02"), "$lt": to.Format("2006-01-02")}})
	if err != nil {
		a.serverError(w, err)
		return
	}
	categories, err := a.docs(r.Context(), "categories", bson.M{"user_id": owner})
	if err != nil {
		a.serverError(w, err)
		return
	}
	byID := map[string]bson.M{}
	for _, c := range categories {
		byID[stringField(c, "id")] = c
	}
	group := map[string]bson.M{}
	for _, e := range expenses {
		c := byID[stringField(e, "category_id")]
		if c == nil {
			continue
		}
		key := stringField(e, "currency") + ":" + stringField(c, "id")
		row := group[key]
		if row == nil {
			row = bson.M{"currency": stringField(e, "currency"), "category_id": stringField(c, "id"), "name": stringField(c, "name"), "kind": stringField(c, "kind"), "total_minor": int64(0), "count": int64(0)}
			group[key] = row
		}
		row["total_minor"] = row["total_minor"].(int64) + int64Field(e, "amount_minor")
		row["count"] = row["count"].(int64) + 1
	}
	data := make([]bson.M, 0, len(group))
	for _, row := range group {
		data = append(data, row)
	}
	sort.Slice(data, func(i, j int) bool { return int64Field(data[i], "total_minor") > int64Field(data[j], "total_minor") })
	respond(w, 200, map[string]any{"month": m.String(), "data": data})
}

func (a *MongoAPI) monthlyReport(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from, to := m.Range()
	data, err := a.moneyTotals(r.Context(), from, to, false)
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"month": m.String(), "data": data})
}

func (a *MongoAPI) annualReport(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	year := m.Time().Year()
	if value := r.URL.Query().Get("year"); value != "" {
		if _, err := time.Parse("2006", value); err != nil {
			badJSON(w, err)
			return
		}
		year, _ = strconv.Atoi(value)
	}
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	a.monthSeries(w, r, from, from.AddDate(1, 0, 0))
}

func (a *MongoAPI) cashFlow(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	a.monthSeries(w, r, m.Add(-11).Time(), m.Next().Time())
}

func (a *MongoAPI) monthSeries(w http.ResponseWriter, r *http.Request, from, to time.Time) {
	data, err := a.moneyTotals(r.Context(), from, to, true)
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"data": data})
}

func (a *MongoAPI) moneyTotals(ctx context.Context, from, to time.Time, monthly bool) ([]bson.M, error) {
	owner := principalFromContext(ctx).String()
	filter := bson.M{"user_id": owner, "occurred_on": bson.M{"$gte": from.Format("2006-01-02"), "$lt": to.Format("2006-01-02")}}
	expenses, err := a.docs(ctx, "expenses", filter)
	if err != nil {
		return nil, err
	}
	income, err := a.docs(ctx, "income", filter)
	if err != nil {
		return nil, err
	}
	group := map[string]bson.M{}
	get := func(doc bson.M) bson.M {
		currency := stringField(doc, "currency")
		key := currency
		var month time.Time
		if monthly {
			date := stringField(doc, "occurred_on")
			if len(date) < 7 {
				return nil
			}
			month = monthDate(date)
			key = month.Format("2006-01-02") + ":" + currency
		}
		row := group[key]
		if row == nil {
			row = bson.M{"currency": currency, "income_minor": int64(0), "borrowed_minor": int64(0), "expenses_minor": int64(0)}
			if monthly {
				row["month"] = month
			}
			group[key] = row
		}
		return row
	}
	for _, e := range expenses {
		row := get(e)
		if row != nil {
			row["expenses_minor"] = row["expenses_minor"].(int64) + int64Field(e, "amount_minor")
		}
	}
	for _, i := range income {
		row := get(i)
		if row != nil {
			field := "income_minor"
			if boolField(i, "is_borrowed") {
				field = "borrowed_minor"
			}
			row[field] = row[field].(int64) + int64Field(i, "amount_minor")
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
		income := row["income_minor"].(int64)
		spent := row["expenses_minor"].(int64)
		if monthly {
			row["earned_minor"] = income
			row["spent_minor"] = spent
			delete(row, "income_minor")
			delete(row, "expenses_minor")
		}
		row["cash_flow_minor"] = income - spent
		data = append(data, row)
	}
	return data, nil
}

func principalFromContext(ctx context.Context) uuid.UUID {
	p, _ := ctx.Value(principalKey{}).(security.Principal)
	return p.UserID
}

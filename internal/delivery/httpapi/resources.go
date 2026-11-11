package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"finfe/internal/domain/money"
)

type field struct {
	kind     string
	required bool
	max      int
	options  []string
}
type resource struct {
	table   string
	fields  map[string]field
	filters map[string]string
}

var resources = map[string]resource{
	"categories":       {"categories", map[string]field{"name": {kind: "text", required: true, max: 60}, "emoji": {kind: "text", max: 16}, "kind": {kind: "enum", required: true, options: []string{"need", "want", "debt", "invest", "saving", "income"}}, "archived": {kind: "bool"}}, nil},
	"expenses":         {"expenses", map[string]field{"occurred_on": {kind: "date", required: true}, "amount_minor": {kind: "positive", required: true}, "currency": {kind: "currency", required: true}, "category_id": {kind: "uuid", required: true}, "note": {kind: "text", max: 500}, "payment_method": {kind: "enum", options: []string{"cash", "mpesa", "card", "bank", "other"}}, "loan_id": {kind: "uuid"}, "goal_id": {kind: "uuid"}, "planned_payment_id": {kind: "uuid"}}, map[string]string{"category_id": "uuid", "payment_method": "text", "from": "date", "to": "date"}},
	"income":           {"income", map[string]field{"occurred_on": {kind: "date", required: true}, "amount_minor": {kind: "positive", required: true}, "currency": {kind: "currency", required: true}, "source": {kind: "text", required: true, max: 60}, "category_id": {kind: "uuid"}, "note": {kind: "text", max: 500}, "is_borrowed": {kind: "bool"}, "loan_id": {kind: "uuid"}}, map[string]string{"source": "text", "from": "date", "to": "date"}},
	"budgets":          {"budgets", map[string]field{"name": {kind: "text", required: true, max: 100}, "period": {kind: "enum", required: true, options: []string{"monthly", "weekly"}}, "amount_minor": {kind: "positive", required: true}, "currency": {kind: "currency", required: true}, "alert_threshold_bp": {kind: "bp"}}, nil},
	"savings":          {"savings_accounts", map[string]field{"name": {kind: "text", required: true, max: 100}, "currency": {kind: "currency", required: true}, "is_emergency_fund": {kind: "bool"}, "target_minor": {kind: "positive"}, "archived": {kind: "bool"}}, nil},
	"goals":            {"goals", map[string]field{"name": {kind: "text", required: true, max: 100}, "emoji": {kind: "text", max: 16}, "target_minor": {kind: "positive", required: true}, "starting_minor": {kind: "nonnegative"}, "currency": {kind: "currency", required: true}, "target_month": {kind: "month"}, "note": {kind: "text", max: 1000}}, nil},
	"loans":            {"loans", map[string]field{"name": {kind: "text", required: true, max: 100}, "lender": {kind: "text", max: 100}, "principal_minor": {kind: "positive", required: true}, "currency": {kind: "currency", required: true}, "interest_rate_bp": {kind: "nonnegative"}, "monthly_payment_minor": {kind: "nonnegative"}, "start_date": {kind: "date", required: true}, "due_day": {kind: "day"}, "closed": {kind: "bool"}, "note": {kind: "text", max: 1000}}, nil},
	"planned-payments": {"planned_payments", map[string]field{"name": {kind: "text", required: true, max: 100}, "amount_minor": {kind: "positive", required: true}, "currency": {kind: "currency", required: true}, "category_id": {kind: "uuid", required: true}, "due_day": {kind: "day"}, "only_month": {kind: "month"}, "from_month": {kind: "month"}, "loan_id": {kind: "uuid"}, "goal_id": {kind: "uuid"}, "active": {kind: "bool"}}, nil},
	"recurring":        {"recurring_transactions", map[string]field{"kind": {kind: "enum", required: true, options: []string{"expense", "income"}}, "name": {kind: "text", required: true, max: 100}, "amount_minor": {kind: "positive", required: true}, "currency": {kind: "currency", required: true}, "category_id": {kind: "uuid"}, "source": {kind: "text", max: 60}, "frequency": {kind: "enum", required: true, options: []string{"weekly", "monthly"}}, "day_of_month": {kind: "day"}, "weekday": {kind: "weekday"}, "start_date": {kind: "date", required: true}, "end_date": {kind: "date"}, "active": {kind: "bool"}}, nil},
}

func (a *API) financeRoutes(r chi.Router) {
	for name, spec := range resources {
		name, spec := name, spec
		r.Route("/"+name, func(r chi.Router) {
			if name == "savings" {
				r.Get("/", a.listSavings)
			} else {
				r.Get("/", a.listResource(spec))
			}
			r.Post("/", a.createResource(spec))
			r.Get("/{id}", a.getResource(spec))
			r.Patch("/{id}", a.updateResource(spec))
			r.Delete("/{id}", a.deleteResource(spec))
			switch name {
			case "expenses":
				r.Get("/summary", a.expenseSummary)
				r.Get("/analytics", a.spendingBreakdown)
			case "budgets":
				r.Get("/{id}/progress", a.budgetProgress)
				r.Put("/{id}/categories", a.setBudgetCategories)
			case "savings":
				r.Get("/{id}/progress", a.savingsProgress)
				r.Get("/{id}/transactions", a.savingsTransactions)
				r.Post("/{id}/transactions", a.addSavingsTransaction)
			case "goals":
				r.Get("/{id}/progress", a.goalProgress)
				r.Post("/{id}/contributions", a.goalContribution)
			}
		})
	}
	r.Get("/reports/monthly", a.monthlyReport)
	r.Get("/reports/annual", a.annualReport)
	r.Get("/reports/cash-flow", a.cashFlow)
	r.Get("/reports/spending-breakdown", a.spendingBreakdown)
	r.Get("/reports/savings-trends", a.savingsTrends)
	r.Get("/financial-health/overview", a.financialHealth)
	r.Get("/financial-health/insights", a.financialHealth)
	r.Get("/financial-health/trends", a.healthTrends)
}

func parseField(name string, spec field, raw json.RawMessage) (any, error) {
	if string(raw) == "null" {
		if spec.required {
			return nil, fmt.Errorf("%s is required", name)
		}
		return nil, nil
	}
	switch spec.kind {
	case "text", "enum", "currency", "uuid", "date", "month":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("%s must be a string", name)
		}
		s = strings.TrimSpace(s)
		if spec.required && s == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
		if spec.max > 0 && len(s) > spec.max {
			return nil, fmt.Errorf("%s is too long", name)
		}
		switch spec.kind {
		case "enum":
			if len(spec.options) > 0 {
				ok := false
				for _, o := range spec.options {
					if s == o {
						ok = true
					}
				}
				if !ok {
					return nil, fmt.Errorf("%s is invalid", name)
				}
			}
		case "currency":
			if !money.ValidCurrency(s) {
				return nil, fmt.Errorf("%s must be a three-letter currency", name)
			}
		case "uuid":
			if _, err := uuid.Parse(s); err != nil {
				return nil, fmt.Errorf("%s must be a UUID", name)
			}
		case "date", "month":
			t, err := parseDate(s)
			if err != nil || (spec.kind == "month" && t.Day() != 1) {
				return nil, fmt.Errorf("%s must be a valid date%s", name, map[bool]string{true: " on the first day of a month", false: ""}[spec.kind == "month"])
			}
		}
		return s, nil
	case "bool":
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("%s must be boolean", name)
		}
		return b, nil
	case "positive", "nonnegative", "bp", "day", "weekday":
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("%s must be an integer", name)
		}
		i, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be an integer", name)
		}
		if spec.kind == "positive" && i <= 0 || spec.kind == "nonnegative" && i < 0 || spec.kind == "bp" && (i < 1000 || i > 10000) || spec.kind == "day" && (i < 0 || i > 31) || spec.kind == "weekday" && (i < 0 || i > 6) {
			return nil, fmt.Errorf("%s is out of range", name)
		}
		return i, nil
	}
	return nil, fmt.Errorf("unsupported field %s", name)
}
func readResource(r *http.Request, s resource, create bool) (map[string]any, error) {
	var raw map[string]json.RawMessage
	if err := decode(r, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("JSON object is required")
	}
	out := map[string]any{}
	for name, value := range raw {
		spec, ok := s.fields[name]
		if !ok {
			return nil, fmt.Errorf("unknown field %s", name)
		}
		v, err := parseField(name, spec, value)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
	if create {
		for name, spec := range s.fields {
			if spec.required {
				if _, ok := out[name]; !ok {
					return nil, fmt.Errorf("%s is required", name)
				}
			}
		}
	}
	return out, nil
}
func dbFailure(a *API, w http.ResponseWriter, err error) {
	var p *pgconn.PgError
	if errorsAs(err, &p) {
		switch p.Code {
		case "23503":
			fail(w, 400, "invalid_reference", "Referenced record does not belong to this account")
			return
		case "23505":
			fail(w, 409, "conflict", "Record already exists")
			return
		case "23514", "23502", "22001":
			fail(w, 400, "validation_failed", "Invalid resource values")
			return
		}
	}
	serverError(a, w, err)
}
func errorsAs(err error, target any) bool             { return errors.As(err, target) }
func collect(rows pgx.Rows) ([]map[string]any, error) { return pgx.CollectRows(rows, pgx.RowToMap) }
func one(ctx context.Context, db *pgxpool.Pool, query string, args ...any) (map[string]any, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectOneRow(rows, pgx.RowToMap)
}
func (a *API) createResource(s resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := readResource(r, s, true)
		if err != nil {
			badJSON(w, err)
			return
		}
		cols := make([]string, 0, len(v))
		for col := range v {
			cols = append(cols, col)
		}
		sort.Strings(cols)
		names := []string{"id", "user_id"}
		args := []any{uuid.New(), principal(r).UserID}
		marks := []string{"$1", "$2"}
		for _, col := range cols {
			names = append(names, col)
			args = append(args, v[col])
			marks = append(marks, fmt.Sprintf("$%d", len(args)))
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *", s.table, strings.Join(names, ","), strings.Join(marks, ","))
		row, err := one(r.Context(), a.db, query, args...)
		if err != nil {
			dbFailure(a, w, err)
			return
		}
		respond(w, 201, map[string]any{"data": row})
	}
}
func (a *API) getResource(s resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := paramID(w, r)
		if !ok {
			return
		}
		row, err := one(r.Context(), a.db, "SELECT * FROM "+s.table+" WHERE id=$1 AND user_id=$2", id, principal(r).UserID)
		if err == pgx.ErrNoRows {
			fail(w, 404, "not_found", "Record not found")
			return
		}
		if err != nil {
			serverError(a, w, err)
			return
		}
		respond(w, 200, map[string]any{"data": row})
	}
}
func (a *API) updateResource(s resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := paramID(w, r)
		if !ok {
			return
		}
		v, err := readResource(r, s, false)
		if err != nil {
			badJSON(w, err)
			return
		}
		if len(v) == 0 {
			fail(w, 400, "validation_failed", "At least one field is required")
			return
		}
		cols := make([]string, 0, len(v))
		for col := range v {
			cols = append(cols, col)
		}
		sort.Strings(cols)
		sets := make([]string, 0, len(v)+1)
		args := []any{id, principal(r).UserID}
		for _, col := range cols {
			args = append(args, v[col])
			sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)))
		}
		sets = append(sets, "updated_at=now()")
		row, err := one(r.Context(), a.db, fmt.Sprintf("UPDATE %s SET %s WHERE id=$1 AND user_id=$2 RETURNING *", s.table, strings.Join(sets, ",")), args...)
		if err == pgx.ErrNoRows {
			fail(w, 404, "not_found", "Record not found")
			return
		}
		if err != nil {
			dbFailure(a, w, err)
			return
		}
		respond(w, 200, map[string]any{"data": row})
	}
}
func (a *API) deleteResource(s resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := paramID(w, r)
		if !ok {
			return
		}
		tag, err := a.db.Exec(r.Context(), "DELETE FROM "+s.table+" WHERE id=$1 AND user_id=$2", id, principal(r).UserID)
		if err != nil {
			var p *pgconn.PgError
			if errors.As(err, &p) && p.Code == "23503" {
				fail(w, 409, "in_use", "Record is still in use")
				return
			}
			dbFailure(a, w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			fail(w, 404, "not_found", "Record not found")
			return
		}
		w.WriteHeader(204)
	}
}
func (a *API) listResource(s resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit < 1 {
			limit = 25
		}
		if limit > 100 {
			limit = 100
		}
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 {
			page = 1
		}
		if page > 100000 {
			fail(w, 400, "invalid_page", "Page is too large")
			return
		}
		where := []string{"user_id=$1"}
		args := []any{principal(r).UserID}
		keys := make([]string, 0, len(s.filters))
		for key := range s.filters {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := r.URL.Query().Get(key)
			if value == "" {
				continue
			}
			kind := s.filters[key]
			if kind == "date" {
				if _, err := parseDate(value); err != nil {
					fail(w, 400, "invalid_filter", "Invalid date filter")
					return
				}
			} else if kind == "uuid" {
				if _, err := uuid.Parse(value); err != nil {
					fail(w, 400, "invalid_filter", "Invalid UUID filter")
					return
				}
			}
			args = append(args, value)
			column := key
			op := "="
			if key == "from" {
				column = "occurred_on"
				op = ">="
			}
			if key == "to" {
				column = "occurred_on"
				op = "<="
			}
			where = append(where, fmt.Sprintf("%s%s$%d", column, op, len(args)))
		}
		if s.table == "expenses" && r.URL.Query().Get("q") != "" {
			args = append(args, "%"+r.URL.Query().Get("q")+"%")
			where = append(where, fmt.Sprintf("note ILIKE $%d", len(args)))
		}
		predicate := strings.Join(where, " AND ")
		var total int64
		if err := a.db.QueryRow(r.Context(), "SELECT count(*) FROM "+s.table+" WHERE "+predicate, args...).Scan(&total); err != nil {
			serverError(a, w, err)
			return
		}
		args = append(args, limit, (page-1)*limit)
		order := "created_at DESC"
		if s.table == "expenses" || s.table == "income" {
			order = "occurred_on DESC,created_at DESC"
		}
		rows, err := a.db.Query(r.Context(), fmt.Sprintf("SELECT * FROM %s WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d", s.table, predicate, order, len(args)-1, len(args)), args...)
		if err != nil {
			serverError(a, w, err)
			return
		}
		data, err := collect(rows)
		if err != nil {
			serverError(a, w, err)
			return
		}
		respond(w, 200, map[string]any{"data": data, "page": page, "limit": limit, "total": total})
	}
}

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

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

package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"finfe/internal/infrastructure/mongodb"
)

func (a *MongoAPI) financeRoutes(r chi.Router) {
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
			if name == "expenses" {
				r.Get("/summary", a.expenseSummary)
				r.Get("/analytics", a.spendingBreakdown)
			}
			switch name {
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

func (a *MongoAPI) resourceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		fail(w, 404, "not_found", "Record not found")
	case errors.Is(err, mongodb.ErrInvalidReference):
		fail(w, 400, "invalid_reference", "Referenced record does not belong to this account")
	case errors.Is(err, mongodb.ErrValidation):
		fail(w, 400, "validation_failed", err.Error())
	case errors.Is(err, mongodb.ErrInUse):
		fail(w, 409, "in_use", "Record is still in use")
	case mongo.IsDuplicateKeyError(err):
		fail(w, 409, "conflict", "Record already exists")
	default:
		a.serverError(w, err)
	}
}

func (a *MongoAPI) createResource(s resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields, err := readResource(r, s, true)
		if err != nil {
			badJSON(w, err)
			return
		}
		row, err := a.resources.Create(r.Context(), s.table, principal(r).UserID, bson.M(fields))
		if err != nil {
			a.resourceError(w, err)
			return
		}
		respond(w, 201, map[string]any{"data": row})
	}
}

func (a *MongoAPI) getResource(s resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := paramID(w, r)
		if !ok {
			return
		}
		row, err := a.resources.Get(r.Context(), s.table, principal(r).UserID, id)
		if err != nil {
			a.resourceError(w, err)
			return
		}
		respond(w, 200, map[string]any{"data": row})
	}
}

func (a *MongoAPI) updateResource(s resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := paramID(w, r)
		if !ok {
			return
		}
		fields, err := readResource(r, s, false)
		if err != nil {
			badJSON(w, err)
			return
		}
		if len(fields) == 0 {
			fail(w, 400, "validation_failed", "At least one field is required")
			return
		}
		row, err := a.resources.Update(r.Context(), s.table, principal(r).UserID, id, bson.M(fields))
		if err != nil {
			a.resourceError(w, err)
			return
		}
		respond(w, 200, map[string]any{"data": row})
	}
}

func (a *MongoAPI) deleteResource(s resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := paramID(w, r)
		if !ok {
			return
		}
		if err := a.resources.Delete(r.Context(), s.table, principal(r).UserID, id); err != nil {
			a.resourceError(w, err)
			return
		}
		w.WriteHeader(204)
	}
}

func (a *MongoAPI) listResource(s resource) http.HandlerFunc {
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
		filter := mongodb.ResourceFilter{Equal: map[string]string{}, Query: r.URL.Query().Get("q")}
		for key, kind := range s.filters {
			value := r.URL.Query().Get(key)
			if value == "" {
				continue
			}
			if kind == "date" {
				if _, err := parseDate(value); err != nil {
					fail(w, 400, "invalid_filter", "Invalid date filter")
					return
				}
			}
			if kind == "uuid" {
				if _, err := uuid.Parse(value); err != nil {
					fail(w, 400, "invalid_filter", "Invalid UUID filter")
					return
				}
			}
			switch key {
			case "from":
				filter.From = value
			case "to":
				filter.To = value
			default:
				filter.Equal[key] = value
			}
		}
		data, total, err := a.resources.List(r.Context(), s.table, principal(r).UserID, filter, page, limit)
		if err != nil {
			a.resourceError(w, err)
			return
		}
		respond(w, 200, map[string]any{"data": data, "page": page, "limit": limit, "total": total})
	}
}

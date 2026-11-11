package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (a *API) setBudgetCategories(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	var v struct {
		CategoryIDs []string `json:"category_ids"`
	}
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	if len(v.CategoryIDs) > 100 {
		fail(w, 400, "validation_failed", "Too many categories")
		return
	}
	seen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(v.CategoryIDs))
	for _, s := range v.CategoryIDs {
		x, err := uuid.Parse(s)
		if err != nil || seen[x] {
			fail(w, 400, "validation_failed", "Invalid or duplicate category ID")
			return
		}
		seen[x] = true
		ids = append(ids, x)
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(a, w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var exists uuid.UUID
	err = tx.QueryRow(r.Context(), `SELECT id FROM budgets WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, principal(r).UserID).Scan(&exists)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Budget not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM budget_categories WHERE budget_id=$1 AND user_id=$2`, id, principal(r).UserID); err != nil {
		serverError(a, w, err)
		return
	}
	for _, cat := range ids {
		if _, err = tx.Exec(r.Context(), `INSERT INTO budget_categories(budget_id,user_id,category_id) VALUES($1,$2,$3)`, id, principal(r).UserID, cat); err != nil {
			dbFailure(a, w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"budget_id": id, "category_ids": ids})
}

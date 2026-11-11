package httpapi

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
)

func (a *API) listSavings(w http.ResponseWriter, r *http.Request) {
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
	var total int64
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM savings_accounts WHERE user_id=$1`, principal(r).UserID).Scan(&total); err != nil {
		serverError(a, w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT a.*,coalesce(t.deposits_minor,0)::bigint AS deposits_minor,coalesce(t.withdrawals_minor,0)::bigint AS withdrawals_minor,(coalesce(t.deposits_minor,0)-coalesce(t.withdrawals_minor,0))::bigint AS balance_minor FROM savings_accounts a LEFT JOIN LATERAL (SELECT sum(amount_minor) FILTER (WHERE kind='deposit') AS deposits_minor,sum(amount_minor) FILTER (WHERE kind='withdrawal') AS withdrawals_minor FROM savings_transactions WHERE account_id=a.id AND user_id=a.user_id) t ON true WHERE a.user_id=$1 ORDER BY a.created_at LIMIT $2 OFFSET $3`, principal(r).UserID, limit, (page-1)*limit)
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
func (a *API) savingsProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	row, err := one(r.Context(), a.db, `SELECT a.id,a.currency,a.target_minor,coalesce(sum(CASE WHEN t.kind='deposit' THEN t.amount_minor ELSE -t.amount_minor END),0)::bigint AS balance_minor FROM savings_accounts a LEFT JOIN savings_transactions t ON t.account_id=a.id AND t.user_id=a.user_id WHERE a.id=$1 AND a.user_id=$2 GROUP BY a.id`, id, principal(r).UserID)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Savings account not found")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, row)
}
func (a *API) savingsTrends(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from := m.Add(-11).Time()
	to := m.Next().Time()
	rows, err := a.db.Query(r.Context(), `SELECT date_trunc('month',t.occurred_on)::date AS month,a.currency,coalesce(sum(t.amount_minor) FILTER (WHERE t.kind='deposit'),0)::bigint AS deposits_minor,coalesce(sum(t.amount_minor) FILTER (WHERE t.kind='withdrawal'),0)::bigint AS withdrawals_minor FROM savings_transactions t JOIN savings_accounts a ON a.id=t.account_id AND a.user_id=t.user_id WHERE t.user_id=$1 AND t.occurred_on >= $2 AND t.occurred_on < $3 GROUP BY 1,2 ORDER BY 1,2`, principal(r).UserID, from, to)
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
func (a *API) healthTrends(w http.ResponseWriter, r *http.Request) {
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	from, to := m.Add(-11).Time(), m.Next().Time()
	rows, err := a.db.Query(r.Context(), `WITH x AS (
      SELECT date_trunc('month',e.occurred_on)::date AS month,e.currency,0::bigint AS earned_minor,sum(e.amount_minor)::bigint AS spent_minor,sum(e.amount_minor) FILTER (WHERE c.kind IN ('saving','invest'))::bigint AS saving_minor FROM expenses e JOIN categories c ON c.id=e.category_id WHERE e.user_id=$1 AND e.occurred_on >= $2 AND e.occurred_on < $3 GROUP BY 1,2
      UNION ALL
      SELECT date_trunc('month',occurred_on)::date,currency,coalesce(sum(amount_minor) FILTER (WHERE NOT is_borrowed),0)::bigint,0::bigint,0::bigint FROM income WHERE user_id=$1 AND occurred_on >= $2 AND occurred_on < $3 GROUP BY 1,2
    ) SELECT month,currency,sum(earned_minor)::bigint AS earned_minor,sum(spent_minor)::bigint AS spent_minor,coalesce(sum(saving_minor),0)::bigint AS saving_minor,(sum(earned_minor)-sum(spent_minor))::bigint AS cash_flow_minor,CASE WHEN sum(earned_minor)>0 THEN (coalesce(sum(saving_minor),0)*10000/sum(earned_minor))::bigint ELSE NULL END AS savings_rate_bp FROM x GROUP BY month,currency ORDER BY month,currency`, principal(r).UserID, from, to)
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

-- name: CreateExpense :one
INSERT INTO expenses (id, user_id, occurred_on, amount_minor, currency, category_id, note, payment_method, loan_id, goal_id, planned_payment_id, recurring_id)
VALUES (@id, @user_id, @occurred_on, @amount_minor, @currency, @category_id, @note, @payment_method, @loan_id, @goal_id, @planned_payment_id, @recurring_id)
RETURNING *;

-- name: GetExpense :one
SELECT * FROM expenses WHERE id = @id AND user_id = @user_id;

-- name: UpdateExpense :one
UPDATE expenses SET
    occurred_on = @occurred_on, amount_minor = @amount_minor, currency = @currency, category_id = @category_id, note = @note,
    payment_method = @payment_method, loan_id = @loan_id, goal_id = @goal_id, planned_payment_id = @planned_payment_id, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteExpense :execrows
DELETE FROM expenses WHERE id = @id AND user_id = @user_id;

-- name: ListExpenses :many
SELECT * FROM expenses
WHERE user_id = @user_id
  AND (sqlc.narg('from')::date IS NULL OR occurred_on >= sqlc.narg('from')::date)
  AND (sqlc.narg('to')::date IS NULL OR occurred_on <= sqlc.narg('to')::date)
  AND (sqlc.narg('category_id')::uuid IS NULL OR category_id = sqlc.narg('category_id')::uuid)
  AND (sqlc.narg('payment_method')::text IS NULL OR payment_method = sqlc.narg('payment_method')::text)
  AND (sqlc.narg('q')::text IS NULL OR note ILIKE '%' || sqlc.narg('q')::text || '%')
ORDER BY occurred_on DESC, created_at DESC
LIMIT @lim OFFSET @off;

-- name: CountExpenses :one
SELECT count(*) FROM expenses
WHERE user_id = @user_id
  AND (sqlc.narg('from')::date IS NULL OR occurred_on >= sqlc.narg('from')::date)
  AND (sqlc.narg('to')::date IS NULL OR occurred_on <= sqlc.narg('to')::date)
  AND (sqlc.narg('category_id')::uuid IS NULL OR category_id = sqlc.narg('category_id')::uuid)
  AND (sqlc.narg('payment_method')::text IS NULL OR payment_method = sqlc.narg('payment_method')::text)
  AND (sqlc.narg('q')::text IS NULL OR note ILIKE '%' || sqlc.narg('q')::text || '%');

-- name: ListAllExpenses :many
SELECT * FROM expenses WHERE user_id = @user_id ORDER BY occurred_on, created_at;

-- name: ExpenseTotals :many
-- Totals for a date range [from, to), one row per currency.
SELECT currency, COALESCE(SUM(amount_minor), 0)::bigint AS total_minor, count(*)::bigint AS count
FROM expenses
WHERE user_id = @user_id AND occurred_on >= @from_date AND occurred_on < @to_date
GROUP BY currency ORDER BY currency;

-- name: ExpensesByCategory :many
SELECT e.currency, c.id AS category_id, c.name, c.emoji, c.kind,
       SUM(e.amount_minor)::bigint AS total_minor, count(*)::bigint AS count
FROM expenses e JOIN categories c ON c.id = e.category_id
WHERE e.user_id = @user_id AND e.occurred_on >= @from_date AND e.occurred_on < @to_date
GROUP BY e.currency, c.id, c.name, c.emoji, c.kind
ORDER BY total_minor DESC;

-- name: ExpensesByMonthAndKind :many
SELECT date_trunc('month', e.occurred_on)::date AS month, e.currency, c.kind, SUM(e.amount_minor)::bigint AS total_minor, count(*)::bigint AS count
FROM expenses e JOIN categories c ON c.id = e.category_id
WHERE e.user_id = @user_id AND e.occurred_on >= @from_date AND e.occurred_on < @to_date
GROUP BY 1, 2, 3
ORDER BY 1;

-- name: ExpensesByPaymentMethod :many
SELECT currency, COALESCE(payment_method, 'unspecified')::text AS payment_method, SUM(amount_minor)::bigint AS total_minor, count(*)::bigint AS count
FROM expenses
WHERE user_id = @user_id AND occurred_on >= @from_date AND occurred_on < @to_date
GROUP BY 1, 2 ORDER BY total_minor DESC;

-- name: TopExpenses :many
SELECT e.*, c.name AS category_name, c.emoji AS category_emoji
FROM expenses e JOIN categories c ON c.id = e.category_id
WHERE e.user_id = @user_id AND e.occurred_on >= @from_date AND e.occurred_on < @to_date
ORDER BY e.amount_minor DESC, e.occurred_on DESC
LIMIT @lim;

-- name: ExpenseEntriesSince :one
SELECT count(*)::bigint AS entries, count(DISTINCT occurred_on)::bigint AS days
FROM expenses WHERE user_id = @user_id AND occurred_on >= @since;

-- name: ExpensesForPlan :many
-- What the month-plan engine needs to match expenses to planned payments.
SELECT e.id, e.amount_minor, e.currency, e.loan_id, e.goal_id, e.planned_payment_id
FROM expenses e
WHERE e.user_id = @user_id AND e.occurred_on >= @from_date AND e.occurred_on < @to_date
ORDER BY e.occurred_on, e.created_at;

-- name: LoanRepayments :many
SELECT loan_id::uuid AS loan_id, SUM(amount_minor)::bigint AS paid_minor
FROM expenses WHERE user_id = @user_id AND loan_id IS NOT NULL
GROUP BY loan_id;

-- name: GoalContributions :many
SELECT goal_id::uuid AS goal_id, SUM(amount_minor)::bigint AS saved_minor, max(occurred_on)::date AS last_on
FROM expenses WHERE user_id = @user_id AND goal_id IS NOT NULL
GROUP BY goal_id;

-- name: SpentInCategories :one
SELECT COALESCE(SUM(amount_minor), 0)::bigint AS spent_minor
FROM expenses
WHERE user_id = @user_id AND category_id = ANY(@category_ids::uuid[]) AND currency = @currency
  AND occurred_on >= @from_date AND occurred_on < @to_date;

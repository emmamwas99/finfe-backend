-- name: CreateIncome :one
INSERT INTO income (id, user_id, occurred_on, amount_minor, currency, source, category_id, note, is_borrowed, loan_id, recurring_id)
VALUES (@id, @user_id, @occurred_on, @amount_minor, @currency, @source, @category_id, @note, @is_borrowed, @loan_id, @recurring_id)
RETURNING *;

-- name: GetIncome :one
SELECT * FROM income WHERE id = @id AND user_id = @user_id;

-- name: UpdateIncome :one
UPDATE income SET
    occurred_on = @occurred_on, amount_minor = @amount_minor, currency = @currency, source = @source, category_id = @category_id,
    note = @note, is_borrowed = @is_borrowed, loan_id = @loan_id, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteIncome :execrows
DELETE FROM income WHERE id = @id AND user_id = @user_id;

-- name: ListIncome :many
SELECT * FROM income
WHERE user_id = @user_id
  AND (sqlc.narg('from')::date IS NULL OR occurred_on >= sqlc.narg('from')::date)
  AND (sqlc.narg('to')::date IS NULL OR occurred_on <= sqlc.narg('to')::date)
  AND (sqlc.narg('source')::text IS NULL OR source = sqlc.narg('source')::text)
  AND (sqlc.narg('borrowed')::bool IS NULL OR is_borrowed = sqlc.narg('borrowed')::bool)
ORDER BY occurred_on DESC, created_at DESC
LIMIT @lim OFFSET @off;

-- name: CountIncome :one
SELECT count(*) FROM income
WHERE user_id = @user_id
  AND (sqlc.narg('from')::date IS NULL OR occurred_on >= sqlc.narg('from')::date)
  AND (sqlc.narg('to')::date IS NULL OR occurred_on <= sqlc.narg('to')::date)
  AND (sqlc.narg('source')::text IS NULL OR source = sqlc.narg('source')::text)
  AND (sqlc.narg('borrowed')::bool IS NULL OR is_borrowed = sqlc.narg('borrowed')::bool);

-- name: ListAllIncome :many
SELECT * FROM income WHERE user_id = @user_id ORDER BY occurred_on, created_at;

-- name: IncomeByMonth :many
-- Earned vs borrowed per month in [from, to), per currency.
SELECT date_trunc('month', occurred_on)::date AS month, currency,
       COALESCE(SUM(amount_minor) FILTER (WHERE NOT is_borrowed), 0)::bigint AS earned_minor,
       COALESCE(SUM(amount_minor) FILTER (WHERE is_borrowed), 0)::bigint AS borrowed_minor,
       count(*)::bigint AS count
FROM income
WHERE user_id = @user_id AND occurred_on >= @from_date AND occurred_on < @to_date
GROUP BY 1, 2 ORDER BY 1;

-- name: IncomeBySource :many
SELECT currency, source, SUM(amount_minor)::bigint AS total_minor, count(*)::bigint AS count, bool_or(is_borrowed) AS any_borrowed
FROM income
WHERE user_id = @user_id AND occurred_on >= @from_date AND occurred_on < @to_date
GROUP BY 1, 2 ORDER BY total_minor DESC;

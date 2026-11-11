-- name: CreateRecurring :one
INSERT INTO recurring_transactions (id, user_id, kind, name, amount_minor, currency, category_id, source, frequency, day_of_month, weekday, start_date, end_date, active)
VALUES (@id, @user_id, @kind, @name, @amount_minor, @currency, @category_id, @source, @frequency, @day_of_month, @weekday, @start_date, @end_date, @active)
RETURNING *;

-- name: GetRecurring :one
SELECT * FROM recurring_transactions WHERE id = @id AND user_id = @user_id;

-- name: UpdateRecurring :one
UPDATE recurring_transactions SET
    name = @name, amount_minor = @amount_minor, currency = @currency, category_id = @category_id, source = @source,
    frequency = @frequency, day_of_month = @day_of_month, weekday = @weekday, start_date = @start_date, end_date = @end_date,
    active = @active, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteRecurring :execrows
DELETE FROM recurring_transactions WHERE id = @id AND user_id = @user_id;

-- name: ListRecurring :many
SELECT * FROM recurring_transactions WHERE user_id = @user_id ORDER BY created_at;

-- name: ListActiveRecurringBatch :many
-- Keyset pagination over every active rule, with the owner's timezone for "today".
SELECT r.*, u.timezone
FROM recurring_transactions r JOIN users u ON u.id = r.user_id
WHERE r.active AND r.id > @after_id
ORDER BY r.id
LIMIT @lim;

-- name: PostRecurringExpense :execrows
INSERT INTO expenses (id, user_id, occurred_on, amount_minor, currency, category_id, note, recurring_id)
VALUES (@id, @user_id, @occurred_on, @amount_minor, @currency, @category_id, @note, @recurring_id)
ON CONFLICT (recurring_id, occurred_on) DO NOTHING;

-- name: PostRecurringIncome :execrows
INSERT INTO income (id, user_id, occurred_on, amount_minor, currency, source, category_id, note, recurring_id)
VALUES (@id, @user_id, @occurred_on, @amount_minor, @currency, @source, @category_id, @note, @recurring_id)
ON CONFLICT (recurring_id, occurred_on) DO NOTHING;

-- name: SetRecurringPosted :exec
UPDATE recurring_transactions SET last_posted_on = @last_posted_on WHERE id = @id;

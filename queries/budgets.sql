-- name: CreateBudget :one
INSERT INTO budgets (id, user_id, name, period, amount_minor, currency, alert_threshold_bp)
VALUES (@id, @user_id, @name, @period, @amount_minor, @currency, @alert_threshold_bp)
RETURNING *;

-- name: GetBudget :one
SELECT * FROM budgets WHERE id = @id AND user_id = @user_id;

-- name: UpdateBudget :one
UPDATE budgets SET name = @name, period = @period, amount_minor = @amount_minor, currency = @currency,
    alert_threshold_bp = @alert_threshold_bp, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteBudget :execrows
DELETE FROM budgets WHERE id = @id AND user_id = @user_id;

-- name: ListBudgets :many
SELECT * FROM budgets WHERE user_id = @user_id ORDER BY created_at;

-- name: ListBudgetCategories :many
SELECT budget_id, category_id FROM budget_categories WHERE user_id = @user_id;

-- name: DeleteBudgetCategories :exec
DELETE FROM budget_categories WHERE budget_id = @budget_id AND user_id = @user_id;

-- name: InsertBudgetCategory :exec
INSERT INTO budget_categories (budget_id, user_id, category_id) VALUES (@budget_id, @user_id, @category_id);

-- name: CreatePlannedPayment :one
INSERT INTO planned_payments (id, user_id, name, amount_minor, currency, category_id, due_day, months, only_month, from_month, loan_id, goal_id, active)
VALUES (@id, @user_id, @name, @amount_minor, @currency, @category_id, @due_day, @months, @only_month, @from_month, @loan_id, @goal_id, @active)
RETURNING *;

-- name: GetPlannedPayment :one
SELECT * FROM planned_payments WHERE id = @id AND user_id = @user_id;

-- name: UpdatePlannedPayment :one
UPDATE planned_payments SET
    name = @name, amount_minor = @amount_minor, currency = @currency, category_id = @category_id, due_day = @due_day, months = @months,
    only_month = @only_month, from_month = @from_month, loan_id = @loan_id, goal_id = @goal_id, active = @active, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeletePlannedPayment :execrows
DELETE FROM planned_payments WHERE id = @id AND user_id = @user_id;

-- name: ListPlannedPayments :many
SELECT * FROM planned_payments WHERE user_id = @user_id ORDER BY due_day, name;

-- name: ListPlannedPaymentOverrides :many
SELECT o.* FROM planned_payment_overrides o JOIN planned_payments p ON p.id = o.planned_payment_id
WHERE p.user_id = @user_id ORDER BY o.month;

-- name: ListPlannedPaymentSkips :many
SELECT s.* FROM planned_payment_skips s JOIN planned_payments p ON p.id = s.planned_payment_id
WHERE p.user_id = @user_id ORDER BY s.month;

-- name: DeletePlannedPaymentOverrides :exec
DELETE FROM planned_payment_overrides WHERE planned_payment_id = @planned_payment_id;

-- name: InsertPlannedPaymentOverride :exec
INSERT INTO planned_payment_overrides (planned_payment_id, month, amount_minor) VALUES (@planned_payment_id, @month, @amount_minor);

-- name: DeletePlannedPaymentSkips :exec
DELETE FROM planned_payment_skips WHERE planned_payment_id = @planned_payment_id;

-- name: InsertPlannedPaymentSkip :exec
INSERT INTO planned_payment_skips (planned_payment_id, month) VALUES (@planned_payment_id, @month);

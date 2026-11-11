-- name: CreateLoan :one
INSERT INTO loans (id, user_id, name, lender, principal_minor, currency, interest_rate_bp, monthly_payment_minor, start_date, due_day, closed, note)
VALUES (@id, @user_id, @name, @lender, @principal_minor, @currency, @interest_rate_bp, @monthly_payment_minor, @start_date, @due_day, @closed, @note)
RETURNING *;

-- name: GetLoan :one
SELECT * FROM loans WHERE id = @id AND user_id = @user_id;

-- name: UpdateLoan :one
UPDATE loans SET
    name = @name, lender = @lender, principal_minor = @principal_minor, currency = @currency, interest_rate_bp = @interest_rate_bp,
    monthly_payment_minor = @monthly_payment_minor, start_date = @start_date, due_day = @due_day, closed = @closed, note = @note, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteLoan :execrows
DELETE FROM loans WHERE id = @id AND user_id = @user_id;

-- name: ListLoans :many
SELECT * FROM loans WHERE user_id = @user_id ORDER BY created_at;

-- name: CreateGoal :one
INSERT INTO goals (id, user_id, name, emoji, target_minor, starting_minor, currency, target_month, note)
VALUES (@id, @user_id, @name, @emoji, @target_minor, @starting_minor, @currency, @target_month, @note)
RETURNING *;

-- name: GetGoal :one
SELECT * FROM goals WHERE id = @id AND user_id = @user_id;

-- name: UpdateGoal :one
UPDATE goals SET
    name = @name, emoji = @emoji, target_minor = @target_minor, starting_minor = @starting_minor, currency = @currency,
    target_month = @target_month, note = @note, completed_at = @completed_at, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteGoal :execrows
DELETE FROM goals WHERE id = @id AND user_id = @user_id;

-- name: ListGoals :many
SELECT * FROM goals WHERE user_id = @user_id ORDER BY created_at;

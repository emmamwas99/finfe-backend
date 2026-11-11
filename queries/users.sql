-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, name, currency, country, timezone, language, expected_income_minor)
VALUES (@id, lower(@email), @password_hash, @name, @currency, @country, @timezone, @language, @expected_income_minor)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(@email);

-- name: UpdateUser :one
UPDATE users SET
    name = @name, currency = @currency, country = @country, timezone = @timezone, language = @language,
    expected_income_minor = @expected_income_minor, warn_at_bp = @warn_at_bp,
    auto_lock_minutes = @auto_lock_minutes, last_backup_at = @last_backup_at, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetUserPassword :exec
UPDATE users SET password_hash = @password_hash, updated_at = now() WHERE id = @id;

-- name: MarkEmailVerified :exec
UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now() WHERE id = @id;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = @id;

-- name: ListIncomeExpectations :many
SELECT * FROM income_expectations WHERE user_id = @user_id ORDER BY month, scope;

-- name: DeleteIncomeExpectations :exec
DELETE FROM income_expectations WHERE user_id = @user_id;

-- name: InsertIncomeExpectation :exec
INSERT INTO income_expectations (user_id, scope, month, amount_minor) VALUES (@user_id, @scope, @month, @amount_minor);

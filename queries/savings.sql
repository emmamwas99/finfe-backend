-- name: CreateSavingsAccount :one
INSERT INTO savings_accounts (id, user_id, name, currency, is_emergency_fund, target_minor)
VALUES (@id, @user_id, @name, @currency, @is_emergency_fund, @target_minor)
RETURNING *;

-- name: GetSavingsAccount :one
SELECT * FROM savings_accounts WHERE id = @id AND user_id = @user_id;

-- name: LockSavingsAccount :one
-- Row lock so concurrent withdrawals can't overdraw the account.
SELECT * FROM savings_accounts WHERE id = @id AND user_id = @user_id FOR UPDATE;

-- name: UpdateSavingsAccount :one
UPDATE savings_accounts SET name = @name, is_emergency_fund = @is_emergency_fund, target_minor = @target_minor,
    archived = @archived, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteSavingsAccount :execrows
DELETE FROM savings_accounts WHERE id = @id AND user_id = @user_id;

-- name: ListSavingsAccountsWithBalance :many
SELECT a.*,
       COALESCE(SUM(t.amount_minor) FILTER (WHERE t.kind = 'deposit'), 0)::bigint AS deposits_minor,
       COALESCE(SUM(t.amount_minor) FILTER (WHERE t.kind = 'withdrawal'), 0)::bigint AS withdrawals_minor
FROM savings_accounts a LEFT JOIN savings_transactions t ON t.account_id = a.id
WHERE a.user_id = @user_id
GROUP BY a.id
ORDER BY a.created_at;

-- name: SavingsAccountBalance :one
SELECT (COALESCE(SUM(amount_minor) FILTER (WHERE kind = 'deposit'), 0) - COALESCE(SUM(amount_minor) FILTER (WHERE kind = 'withdrawal'), 0))::bigint AS balance_minor
FROM savings_transactions WHERE account_id = @account_id AND user_id = @user_id;

-- name: CreateSavingsTransaction :one
INSERT INTO savings_transactions (id, user_id, account_id, kind, amount_minor, occurred_on, note)
VALUES (@id, @user_id, @account_id, @kind, @amount_minor, @occurred_on, @note)
RETURNING *;

-- name: ListSavingsTransactions :many
SELECT * FROM savings_transactions WHERE account_id = @account_id AND user_id = @user_id
ORDER BY occurred_on DESC, created_at DESC LIMIT @lim OFFSET @off;

-- name: CountSavingsTransactions :one
SELECT count(*) FROM savings_transactions WHERE account_id = @account_id AND user_id = @user_id;

-- name: EmergencyFundBalance :one
SELECT (COALESCE(SUM(t.amount_minor) FILTER (WHERE t.kind = 'deposit'), 0) - COALESCE(SUM(t.amount_minor) FILTER (WHERE t.kind = 'withdrawal'), 0))::bigint AS balance_minor,
       count(DISTINCT a.id)::bigint AS accounts
FROM savings_accounts a LEFT JOIN savings_transactions t ON t.account_id = a.id
WHERE a.user_id = @user_id AND a.is_emergency_fund AND NOT a.archived AND a.currency = @currency;

-- name: SavingsFlowByMonth :many
SELECT date_trunc('month', t.occurred_on)::date AS month, a.currency,
       COALESCE(SUM(t.amount_minor) FILTER (WHERE t.kind = 'deposit'), 0)::bigint AS deposits_minor,
       COALESCE(SUM(t.amount_minor) FILTER (WHERE t.kind = 'withdrawal'), 0)::bigint AS withdrawals_minor
FROM savings_transactions t JOIN savings_accounts a ON a.id = t.account_id
WHERE t.user_id = @user_id AND t.occurred_on >= @from_date AND t.occurred_on < @to_date
GROUP BY 1, 2 ORDER BY 1;

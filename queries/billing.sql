-- name: CreateSubscription :exec
INSERT INTO subscriptions (user_id, status, trial_ends_at) VALUES (@user_id, 'trialing', @trial_ends_at)
ON CONFLICT (user_id) DO NOTHING;

-- name: GetSubscription :one
SELECT * FROM subscriptions WHERE user_id = @user_id;

-- name: LockSubscription :one
SELECT * FROM subscriptions WHERE user_id = @user_id FOR UPDATE;

-- name: ExtendSubscription :exec
UPDATE subscriptions SET current_period_end = @current_period_end, status = 'active', billing = @billing, period = @period,
    paystack_customer_code = COALESCE(sqlc.narg('customer_code'), paystack_customer_code), updated_at = now()
WHERE user_id = @user_id;

-- name: UpdateSubscriptionFromWebhook :exec
UPDATE subscriptions SET
    status = @status,
    billing = 'card',
    paystack_subscription_code = COALESCE(sqlc.narg('subscription_code'), paystack_subscription_code),
    paystack_email_token = COALESCE(sqlc.narg('email_token'), paystack_email_token),
    paystack_customer_code = COALESCE(sqlc.narg('customer_code'), paystack_customer_code),
    current_period_end = COALESCE(sqlc.narg('current_period_end'), current_period_end),
    updated_at = now()
WHERE user_id = @user_id;

-- name: SetSubscriptionStatus :exec
UPDATE subscriptions SET status = @status, updated_at = now() WHERE user_id = @user_id;

-- name: FindUserByCustomerCode :one
SELECT user_id FROM subscriptions WHERE paystack_customer_code = @customer_code LIMIT 1;

-- name: InsertPendingPayment :exec
INSERT INTO payments (user_id, reference, amount_minor, currency, period, method, status)
VALUES (@user_id, @reference, @amount_minor, @currency, @period, @method, 'pending');

-- name: GetPayment :one
SELECT * FROM payments WHERE reference = @reference;

-- name: LockPayment :one
SELECT * FROM payments WHERE reference = @reference FOR UPDATE;

-- name: UpsertSuccessfulPayment :exec
INSERT INTO payments (user_id, reference, amount_minor, currency, period, method, channel, status, paid_at)
VALUES (@user_id, @reference, @amount_minor, @currency, @period, @method, @channel, 'success', @paid_at)
ON CONFLICT (reference) DO UPDATE SET
    status = 'success', amount_minor = EXCLUDED.amount_minor, channel = EXCLUDED.channel, paid_at = EXCLUDED.paid_at,
    user_id = COALESCE(payments.user_id, EXCLUDED.user_id);

-- name: MarkPaymentFailed :exec
UPDATE payments SET status = 'failed' WHERE reference = @reference AND status = 'pending';

-- name: ListPayments :many
SELECT * FROM payments WHERE user_id = @user_id ORDER BY created_at DESC LIMIT 50;

-- name: InsertWebhookEvent :execrows
INSERT INTO webhook_events (id) VALUES (@id) ON CONFLICT (id) DO NOTHING;

-- name: DeleteWebhookEvent :exec
DELETE FROM webhook_events WHERE id = @id;

-- name: CreateSession :one
INSERT INTO user_sessions (id, user_id, user_agent, ip, expires_at)
VALUES (@id, @user_id, @user_agent, @ip, @expires_at)
RETURNING *;

-- name: GetActiveSession :one
SELECT * FROM user_sessions WHERE id = @id AND revoked_at IS NULL AND expires_at > now();

-- name: TouchSession :exec
UPDATE user_sessions SET last_used_at = now(), expires_at = @expires_at WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeSession :execrows
UPDATE user_sessions SET revoked_at = now() WHERE id = @id AND user_id = @user_id AND revoked_at IS NULL;

-- name: RevokeAllSessions :exec
UPDATE user_sessions SET revoked_at = now() WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: ListActiveSessions :many
SELECT * FROM user_sessions
WHERE user_id = @user_id AND revoked_at IS NULL AND expires_at > now()
ORDER BY last_used_at DESC;

-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at) VALUES (@id, @session_id, @token_hash, @expires_at);

-- name: GetRefreshToken :one
SELECT rt.id, rt.session_id, rt.expires_at, rt.used_at, s.user_id, s.revoked_at AS session_revoked_at, s.expires_at AS session_expires_at
FROM refresh_tokens rt JOIN user_sessions s ON s.id = rt.session_id
WHERE rt.token_hash = @token_hash;

-- Atomically claims a refresh token: returns no row if it was already used (rotation / reuse detection).
-- name: UseRefreshToken :one
UPDATE refresh_tokens SET used_at = now() WHERE id = @id AND used_at IS NULL RETURNING id;

-- name: CreateEmailVerification :exec
INSERT INTO email_verifications (token_hash, user_id, expires_at) VALUES (@token_hash, @user_id, @expires_at);

-- name: UseEmailVerification :one
UPDATE email_verifications SET used_at = now()
WHERE token_hash = @token_hash AND used_at IS NULL AND expires_at > now()
RETURNING user_id;

-- name: CreatePasswordReset :exec
INSERT INTO password_resets (token_hash, user_id, expires_at) VALUES (@token_hash, @user_id, @expires_at);

-- name: UsePasswordReset :one
UPDATE password_resets SET used_at = now()
WHERE token_hash = @token_hash AND used_at IS NULL AND expires_at > now()
RETURNING user_id;

-- name: InvalidatePasswordResets :exec
UPDATE password_resets SET used_at = now() WHERE user_id = @user_id AND used_at IS NULL;

-- name: InsertAuditEvent :exec
INSERT INTO audit_events (user_id, action, ip, user_agent, metadata) VALUES (@user_id, @action, @ip, @user_agent, @metadata);

-- name: DeleteExpiredAuthRecords :exec
WITH s AS (DELETE FROM user_sessions WHERE expires_at < now() - interval '7 days' OR revoked_at < now() - interval '7 days'),
     e AS (DELETE FROM email_verifications WHERE expires_at < now() - interval '7 days'),
     p AS (DELETE FROM password_resets WHERE expires_at < now() - interval '7 days')
DELETE FROM refresh_tokens WHERE expires_at < now() - interval '7 days';

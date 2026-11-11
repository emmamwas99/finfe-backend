package httpapi

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"finfe/internal/infrastructure/database"
	"finfe/internal/infrastructure/email"
	"finfe/internal/infrastructure/persistence/store"
	"finfe/internal/infrastructure/security"
)

func (a *API) limited(w http.ResponseWriter, r *http.Request, action string) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	ok, err := a.limiter.Allow(r.Context(), action+":"+ip, 10, 15*time.Minute)
	if err != nil {
		a.log.Warn("rate limiter", "error", err)
	}
	if !ok {
		w.Header().Set("Retry-After", "900")
		fail(w, 429, "rate_limited", "Too many attempts. Try later.")
		return true
	}
	return false
}

func publicUser(u store.User) map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "currency": strings.TrimSpace(u.Currency), "country": strings.TrimSpace(u.Country), "timezone": u.Timezone, "language": u.Language, "expectedIncomeMinor": u.ExpectedIncomeMinor, "warnAtBp": u.WarnAtBp, "autoLockMinutes": u.AutoLockMinutes, "emailVerified": u.EmailVerifiedAt != nil}
}

func (a *API) setCookies(w http.ResponseWriter, access, refresh string, accessExpiry, refreshExpiry time.Time) {
	base := http.Cookie{Path: "/", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, Domain: a.cfg.CookieDomain}
	ac := base
	ac.Name = "finfe_access"
	ac.Value = access
	ac.Expires = accessExpiry
	ac.MaxAge = int(time.Until(accessExpiry).Seconds())
	http.SetCookie(w, &ac)
	rc := base
	rc.Name = "finfe_refresh"
	rc.Value = refresh
	rc.Path = "/api/v1/auth"
	rc.Expires = refreshExpiry
	rc.MaxAge = int(time.Until(refreshExpiry).Seconds())
	http.SetCookie(w, &rc)
}
func (a *API) clearCookies(w http.ResponseWriter) {
	for _, x := range []struct{ name, path string }{{"finfe_access", "/"}, {"finfe_refresh", "/api/v1/auth"}} {
		http.SetCookie(w, &http.Cookie{Name: x.name, Path: x.path, Domain: a.cfg.CookieDomain, HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	}
}

func (a *API) newSession(ctx context.Context, w http.ResponseWriter, r *http.Request, uid uuid.UUID) error {
	now := time.Now().UTC()
	sid := uuid.New()
	refresh, hash, err := security.RandomToken()
	if err != nil {
		return err
	}
	access, accessExpiry, err := security.IssueAccessToken(a.cfg.JWTSecret, security.Principal{UserID: uid, SessionID: sid}, a.cfg.AccessTokenTTL, now)
	if err != nil {
		return err
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if _, err = q.CreateSession(ctx, store.CreateSessionParams{ID: sid, UserID: uid, UserAgent: r.UserAgent(), Ip: r.RemoteAddr, ExpiresAt: now.Add(a.cfg.RefreshTokenTTL)}); err != nil {
		return err
	}
	if err = q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{ID: uuid.New(), SessionID: sid, TokenHash: hash, ExpiresAt: now.Add(a.cfg.RefreshTokenTTL)}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	a.setCookies(w, access, refresh, accessExpiry, now.Add(a.cfg.RefreshTokenTTL))
	return nil
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "register") {
		return
	}
	var v struct{ Email, Password, Name string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	v.Email = strings.ToLower(strings.TrimSpace(v.Email))
	v.Name = strings.TrimSpace(v.Name)
	if !strings.Contains(v.Email, "@") || len(v.Email) > 254 || len(v.Password) < 12 || len(v.Name) > 100 {
		fail(w, 400, "validation_failed", "Enter a valid email, password of at least 12 characters, and name under 100 characters")
		return
	}
	hash, err := security.HashPassword(v.Password, security.DefaultArgon2)
	if err != nil {
		serverError(a, w, err)
		return
	}
	uid := uuid.New()
	now := time.Now().UTC()
	verifyToken, verifyHash, err := security.RandomToken()
	if err != nil {
		serverError(a, w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(a, w, err)
		return
	}
	defer tx.Rollback(r.Context())
	q := store.New(tx)
	u, err := q.CreateUser(r.Context(), store.CreateUserParams{ID: uid, Email: v.Email, PasswordHash: hash, Name: v.Name, Currency: "KES", Country: "KE", Timezone: "Africa/Nairobi", Language: "en"})
	if err != nil {
		if code, _, ok := database.Constraint(err); ok && code == database.UniqueViolation {
			fail(w, 409, "email_in_use", "An account with this email already exists")
		} else {
			serverError(a, w, err)
		}
		return
	}
	trialEnd := now.AddDate(0, 0, a.cfg.TrialDays)
	if err = q.CreateSubscription(r.Context(), store.CreateSubscriptionParams{UserID: uid, TrialEndsAt: &trialEnd}); err != nil {
		serverError(a, w, err)
		return
	}
	if a.cfg.RequireEmailVerification {
		if err = q.CreateEmailVerification(r.Context(), store.CreateEmailVerificationParams{TokenHash: verifyHash, UserID: uid, ExpiresAt: now.Add(24 * time.Hour)}); err != nil {
			serverError(a, w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(a, w, err)
		return
	}
	a.audit(r, uid, "account.register")
	if a.cfg.RequireEmailVerification {
		if err = a.mail.Send(r.Context(), email.Verification(a.cfg.AppURL, v.Email, v.Name, verifyToken)); err != nil {
			a.log.Error("verification email failed", "error", err)
		}
		respond(w, 201, map[string]any{"user": publicUser(u), "needsConfirmation": true})
		return
	}
	if err = a.newSession(r.Context(), w, r, uid); err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 201, map[string]any{"user": publicUser(u), "needsConfirmation": false})
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "login") {
		return
	}
	var v struct{ Email, Password string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	u, err := a.q.GetUserByEmail(r.Context(), strings.ToLower(strings.TrimSpace(v.Email)))
	if err != nil {
		fail(w, 401, "invalid_credentials", "Wrong email or password")
		return
	}
	_, err = security.VerifyPassword(v.Password, u.PasswordHash)
	if err != nil {
		fail(w, 401, "invalid_credentials", "Wrong email or password")
		return
	}
	if a.cfg.RequireEmailVerification && u.EmailVerifiedAt == nil {
		fail(w, 403, "email_unverified", "Please confirm your email first")
		return
	}
	if err = a.newSession(r.Context(), w, r, u.ID); err != nil {
		serverError(a, w, err)
		return
	}
	a.audit(r, u.ID, "session.login")
	respond(w, 200, map[string]any{"user": publicUser(u)})
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("finfe_refresh")
	if err != nil {
		fail(w, 401, "unauthenticated", "Sign in again")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(a, w, err)
		return
	}
	defer tx.Rollback(ctx)
	var tokenID, sid, uid uuid.UUID
	var used, revoked *time.Time
	var expires, sessionExpires time.Time
	err = tx.QueryRow(ctx, `SELECT rt.id, rt.session_id, s.user_id, rt.used_at, s.revoked_at, rt.expires_at, s.expires_at FROM refresh_tokens rt JOIN user_sessions s ON s.id=rt.session_id WHERE rt.token_hash=$1 FOR UPDATE`, security.HashToken(c.Value)).Scan(&tokenID, &sid, &uid, &used, &revoked, &expires, &sessionExpires)
	if err != nil || revoked != nil || time.Now().After(expires) || time.Now().After(sessionExpires) {
		fail(w, 401, "unauthenticated", "Session expired")
		return
	}
	if used != nil {
		// A second tab can race an ordinary refresh. Allow a brief grace window
		// before treating an old token as theft and revoking the session.
		if time.Since(*used) > 5*time.Second {
			_, _ = tx.Exec(ctx, `UPDATE user_sessions SET revoked_at=now() WHERE id=$1`, sid)
			_ = tx.Commit(ctx)
			a.clearCookies(w)
		}
		fail(w, 401, "token_reused", "Refresh token already used")
		return
	}
	refresh, hash, err := security.RandomToken()
	if err != nil {
		serverError(a, w, err)
		return
	}
	now := time.Now().UTC()
	expiry := now.Add(a.cfg.RefreshTokenTTL)
	if _, err = tx.Exec(ctx, `UPDATE refresh_tokens SET used_at=now() WHERE id=$1`, tokenID); err != nil {
		serverError(a, w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO refresh_tokens(id,session_id,token_hash,expires_at) VALUES($1,$2,$3,$4)`, uuid.New(), sid, hash, expiry); err != nil {
		serverError(a, w, err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE user_sessions SET last_used_at=now(),expires_at=$2 WHERE id=$1`, sid, expiry); err != nil {
		serverError(a, w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(a, w, err)
		return
	}
	access, accessExpiry, err := security.IssueAccessToken(a.cfg.JWTSecret, security.Principal{UserID: uid, SessionID: sid}, a.cfg.AccessTokenTTL, now)
	if err != nil {
		serverError(a, w, err)
		return
	}
	a.setCookies(w, access, refresh, accessExpiry, expiry)
	respond(w, 200, map[string]any{"ok": true})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	_, err := a.q.RevokeSession(r.Context(), store.RevokeSessionParams{ID: p.SessionID, UserID: p.UserID})
	if err != nil {
		serverError(a, w, err)
		return
	}
	a.audit(r, p.UserID, "session.logout")
	a.clearCookies(w)
	w.WriteHeader(204)
}

func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := a.q.ListActiveSessions(r.Context(), principal(r).UserID)
	if err != nil {
		serverError(a, w, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, s := range rows {
		data = append(data, map[string]any{"id": s.ID, "userAgent": s.UserAgent, "createdAt": s.CreatedAt, "lastUsedAt": s.LastUsedAt, "current": s.ID == principal(r).SessionID})
	}
	respond(w, 200, map[string]any{"sessions": data})
}
func (a *API) revokeSession(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	affected, err := a.q.RevokeSession(r.Context(), store.RevokeSessionParams{ID: id, UserID: principal(r).UserID})
	if err != nil {
		serverError(a, w, err)
		return
	}
	if affected == 0 {
		fail(w, 404, "not_found", "Session not found")
		return
	}
	if id == principal(r).SessionID {
		a.clearCookies(w)
	}
	a.audit(r, principal(r).UserID, "session.revoke")
	w.WriteHeader(204)
}

func (a *API) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "verify") {
		return
	}
	var v struct{ Token string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(a, w, err)
		return
	}
	defer tx.Rollback(r.Context())
	q := store.New(tx)
	uid, err := q.UseEmailVerification(r.Context(), security.HashToken(v.Token))
	if err != nil {
		fail(w, 400, "invalid_token", "Link expired or already used")
		return
	}
	if err = q.MarkEmailVerified(r.Context(), uid); err != nil {
		serverError(a, w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(a, w, err)
		return
	}
	a.audit(r, uid, "account.verify_email")
	respond(w, 200, map[string]any{"ok": true})
}

func (a *API) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "forgot") {
		return
	}
	var v struct{ Email string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	u, err := a.q.GetUserByEmail(r.Context(), strings.TrimSpace(v.Email))
	if err == nil {
		token, hash, e := security.RandomToken()
		if e == nil {
			e = a.q.CreatePasswordReset(r.Context(), store.CreatePasswordResetParams{TokenHash: hash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
		}
		if e == nil {
			e = a.mail.Send(r.Context(), email.PasswordReset(a.cfg.AppURL, u.Email, u.Name, token))
		}
		if e != nil {
			a.log.Error("password reset email failed", "error", e)
		}
	}
	respond(w, 200, map[string]any{"ok": true})
}

func (a *API) recoverySession(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "recovery") {
		return
	}
	var v struct{ Token string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	uid, err := a.q.UsePasswordReset(r.Context(), security.HashToken(v.Token))
	if err != nil {
		fail(w, 400, "invalid_token", "Link expired or already used")
		return
	}
	u, err := a.q.GetUserByID(r.Context(), uid)
	if err != nil {
		serverError(a, w, err)
		return
	}
	if err = a.newSession(r.Context(), w, r, uid); err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"user": publicUser(u)})
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var v struct{ Password string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	if len(v.Password) < 12 {
		fail(w, 400, "validation_failed", "Password is too short")
		return
	}
	hash, err := security.HashPassword(v.Password, security.DefaultArgon2)
	if err != nil {
		serverError(a, w, err)
		return
	}
	p := principal(r)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(a, w, err)
		return
	}
	defer tx.Rollback(r.Context())
	q := store.New(tx)
	if err = q.SetUserPassword(r.Context(), store.SetUserPasswordParams{ID: p.UserID, PasswordHash: hash}); err != nil {
		serverError(a, w, err)
		return
	}
	if err = q.InvalidatePasswordResets(r.Context(), p.UserID); err != nil {
		serverError(a, w, err)
		return
	}
	// Existing session stays alive; all other sessions are revoked.
	_, err = tx.Exec(r.Context(), `UPDATE user_sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2`, p.UserID, p.SessionID)
	if err != nil {
		serverError(a, w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(a, w, err)
		return
	}
	a.audit(r, p.UserID, "account.change_password")
	respond(w, 200, map[string]any{"ok": true})
}

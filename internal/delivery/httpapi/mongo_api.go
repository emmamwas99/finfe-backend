package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"finfe/internal/config"
	"finfe/internal/domain/model"
	"finfe/internal/domain/money"
	"finfe/internal/infrastructure/email"
	"finfe/internal/infrastructure/mongodb"
	"finfe/internal/infrastructure/paystack"
	"finfe/internal/infrastructure/security"
)

// MongoAPI is the MongoDB-backed route implementation.
type MongoAPI struct {
	*API
	database  *mongodb.Database
	authStore mongodb.AuthStore
	resources mongodb.ResourceStore
	vaults    mongoVaultStore
}

func NewMongo(cfg config.Config, db *mongodb.Database, mail email.Mailer, log *slog.Logger) *MongoAPI {
	return &MongoAPI{API: newBase(cfg, mail, log), database: db, authStore: mongodb.AuthStore{DB: db.DB}, resources: mongodb.ResourceStore{DB: db.DB}, vaults: mongoVaultStore{db: db.DB}}
}

func (a *MongoAPI) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, a.logRequests, middleware.Recoverer)
	if a.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(a.cors, a.originGuard)
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/ready", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := a.database.Client.Ping(ctx, nil); err != nil {
			fail(w, 503, "unavailable", "Database is unavailable")
			return
		}
		respond(w, 200, map[string]string{"status": "ready"})
	})
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/billing/webhook", a.billingWebhook)
		r.Post("/auth/register", a.register)
		r.Post("/auth/login", a.login)
		r.Post("/auth/refresh", a.refresh)
		r.Post("/auth/forgot-password", a.forgotPassword)
		r.Post("/auth/recovery-session", a.recoverySession)
		r.Post("/auth/verify-email", a.verifyEmail)
		r.Group(func(r chi.Router) {
			r.Use(a.auth)
			r.Post("/auth/logout", a.logout)
			r.Post("/auth/password", a.changePassword)
			r.Get("/auth/sessions", a.listSessions)
			r.Delete("/auth/sessions/{id}", a.revokeSession)
			r.Get("/users/me", a.me)
			r.Patch("/users/me", a.updateMe)
			r.Delete("/users/me", a.deleteMe)
			r.Get("/vault", a.getVault)
			r.Post("/vault", a.createVault)
			r.Patch("/vault", a.saveVault)
			r.Get("/billing/subscription", a.billingSubscription)
			r.Get("/billing/payments", a.billingPayments)
			r.Post("/billing/checkout", a.billingCheckout)
			r.Post("/billing/verify", a.billingVerify)
			r.Get("/billing/manage", a.billingManage)
			r.Get("/billing/demo/{reference}", a.billingDemoInfo)
			r.Post("/billing/demo/{reference}", a.billingDemoComplete)
			a.financeRoutes(r)
		})
	})
	return r
}

func (a *MongoAPI) serverError(w http.ResponseWriter, err error) {
	a.log.Error("request failed", "error", err)
	fail(w, 500, "internal_error", "Something went wrong")
}

func (a *MongoAPI) audit(r *http.Request, uid uuid.UUID, action string) {
	if err := a.authStore.InsertAuditEvent(r.Context(), model.InsertAuditEventParams{UserID: &uid, Action: action, Ip: r.RemoteAddr, UserAgent: r.UserAgent(), Metadata: []byte("{}")}); err != nil {
		a.log.Error("audit event failed", "action", action, "error", err)
	}
}

func (a *MongoAPI) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if c, err := r.Cookie("finfe_access"); err == nil {
			token = c.Value
		}
		if token == "" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		p, err := security.ParseAccessToken(a.cfg.JWTSecret, token)
		if err != nil {
			fail(w, 401, "unauthenticated", "Sign in again")
			return
		}
		session, err := a.authStore.GetActiveSession(r.Context(), p.SessionID)
		if err != nil || session.UserID != p.UserID {
			fail(w, 401, "unauthenticated", "Session expired")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

func (a *MongoAPI) newSession(ctx context.Context, w http.ResponseWriter, r *http.Request, uid uuid.UUID) error {
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
	expiry := now.Add(a.cfg.RefreshTokenTTL)
	if err := a.database.InTx(ctx, func(tx context.Context) error {
		if _, err := a.authStore.CreateSession(tx, model.CreateSessionParams{ID: sid, UserID: uid, UserAgent: r.UserAgent(), Ip: r.RemoteAddr, ExpiresAt: expiry}); err != nil {
			return err
		}
		return a.authStore.CreateRefreshToken(tx, model.CreateRefreshTokenParams{ID: uuid.New(), SessionID: sid, TokenHash: hash, ExpiresAt: expiry})
	}); err != nil {
		return err
	}
	a.setCookies(w, access, refresh, accessExpiry, expiry)
	return nil
}

func (a *MongoAPI) register(w http.ResponseWriter, r *http.Request) {
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
		a.serverError(w, err)
		return
	}
	uid := uuid.New()
	verifyToken, verifyHash, err := security.RandomToken()
	if err != nil {
		a.serverError(w, err)
		return
	}
	var user model.User
	err = a.database.InTx(r.Context(), func(tx context.Context) error {
		var err error
		user, err = a.authStore.CreateUser(tx, model.CreateUserParams{ID: uid, Email: v.Email, PasswordHash: hash, Name: v.Name, Currency: "KES", Country: "KE", Timezone: "Africa/Nairobi", Language: "en"})
		if err != nil {
			return err
		}
		trialEnd := time.Now().UTC().AddDate(0, 0, a.cfg.TrialDays)
		if err := a.authStore.CreateSubscription(tx, model.CreateSubscriptionParams{UserID: uid, TrialEndsAt: &trialEnd}); err != nil {
			return err
		}
		if a.cfg.RequireEmailVerification {
			return a.authStore.CreateEmailVerification(tx, model.CreateEmailVerificationParams{TokenHash: verifyHash, UserID: uid, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)})
		}
		return nil
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			fail(w, 409, "email_in_use", "An account with this email already exists")
		} else {
			a.serverError(w, err)
		}
		return
	}
	a.audit(r, uid, "account.register")
	if a.cfg.RequireEmailVerification {
		if err := a.mail.Send(r.Context(), email.Verification(a.cfg.AppURL, v.Email, v.Name, verifyToken)); err != nil {
			a.log.Error("verification email failed", "error", err)
		}
		respond(w, 201, map[string]any{"user": publicUser(user), "needsConfirmation": true})
		return
	}
	if err := a.newSession(r.Context(), w, r, uid); err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"user": publicUser(user), "needsConfirmation": false})
}

func (a *MongoAPI) login(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "login") {
		return
	}
	var v struct{ Email, Password string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	u, err := a.authStore.GetUserByEmail(r.Context(), v.Email)
	if err != nil {
		fail(w, 401, "invalid_credentials", "Wrong email or password")
		return
	}
	if _, err := security.VerifyPassword(v.Password, u.PasswordHash); err != nil {
		fail(w, 401, "invalid_credentials", "Wrong email or password")
		return
	}
	if a.cfg.RequireEmailVerification && u.EmailVerifiedAt == nil {
		fail(w, 403, "email_unverified", "Please confirm your email first")
		return
	}
	if err := a.newSession(r.Context(), w, r, u.ID); err != nil {
		a.serverError(w, err)
		return
	}
	a.audit(r, u.ID, "session.login")
	respond(w, 200, map[string]any{"user": publicUser(u)})
}

func (a *MongoAPI) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("finfe_refresh")
	if err != nil {
		fail(w, 401, "unauthenticated", "Sign in again")
		return
	}
	var uid, sid uuid.UUID
	var refresh string
	var hash []byte
	var reused bool
	var revoked bool
	now := time.Now().UTC()
	expiry := now.Add(a.cfg.RefreshTokenTTL)
	err = a.database.InTx(r.Context(), func(tx context.Context) error {
		row, err := a.authStore.GetRefreshToken(tx, security.HashToken(cookie.Value))
		if err != nil {
			return err
		}
		uid, sid = row.UserID, row.SessionID
		if row.SessionRevokedAt != nil || now.After(row.ExpiresAt) || now.After(row.SessionExpiresAt) {
			return mongo.ErrNoDocuments
		}
		if row.UsedAt != nil {
			reused = true
			if now.Sub(*row.UsedAt) > 5*time.Second {
				revoked = true
				_, err = a.authStore.RevokeSession(tx, model.RevokeSessionParams{ID: sid, UserID: uid})
			}
			return err
		}
		refresh, hash, err = security.RandomToken()
		if err != nil {
			return err
		}
		if _, err := a.authStore.UseRefreshToken(tx, row.ID); err != nil {
			return err
		}
		if err := a.authStore.CreateRefreshToken(tx, model.CreateRefreshTokenParams{ID: uuid.New(), SessionID: sid, TokenHash: hash, ExpiresAt: expiry}); err != nil {
			return err
		}
		return a.authStore.TouchSession(tx, model.TouchSessionParams{ID: sid, ExpiresAt: expiry})
	})
	if err != nil {
		fail(w, 401, "unauthenticated", "Session expired")
		return
	}
	if reused {
		if revoked {
			a.clearCookies(w)
		}
		fail(w, 401, "token_reused", "Refresh token already used")
		return
	}
	access, accessExpiry, err := security.IssueAccessToken(a.cfg.JWTSecret, security.Principal{UserID: uid, SessionID: sid}, a.cfg.AccessTokenTTL, now)
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.setCookies(w, access, refresh, accessExpiry, expiry)
	respond(w, 200, map[string]any{"ok": true})
}

func (a *MongoAPI) logout(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if _, err := a.authStore.RevokeSession(r.Context(), model.RevokeSessionParams{ID: p.SessionID, UserID: p.UserID}); err != nil {
		a.serverError(w, err)
		return
	}
	a.audit(r, p.UserID, "session.logout")
	a.clearCookies(w)
	w.WriteHeader(204)
}

func (a *MongoAPI) listSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := a.authStore.ListActiveSessions(r.Context(), principal(r).UserID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, s := range rows {
		data = append(data, map[string]any{"id": s.ID, "userAgent": s.UserAgent, "createdAt": s.CreatedAt, "lastUsedAt": s.LastUsedAt, "current": s.ID == principal(r).SessionID})
	}
	respond(w, 200, map[string]any{"sessions": data})
}

func (a *MongoAPI) revokeSession(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	affected, err := a.authStore.RevokeSession(r.Context(), model.RevokeSessionParams{ID: id, UserID: principal(r).UserID})
	if err != nil {
		a.serverError(w, err)
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

func (a *MongoAPI) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "verify") {
		return
	}
	var v struct{ Token string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	var uid uuid.UUID
	err := a.database.InTx(r.Context(), func(tx context.Context) error {
		var err error
		uid, err = a.authStore.UseEmailVerification(tx, security.HashToken(v.Token))
		if err != nil {
			return err
		}
		return a.authStore.MarkEmailVerified(tx, uid)
	})
	if errors.Is(err, mongo.ErrNoDocuments) {
		fail(w, 400, "invalid_token", "Link expired or already used")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.audit(r, uid, "account.verify_email")
	respond(w, 200, map[string]any{"ok": true})
}

func (a *MongoAPI) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "forgot") {
		return
	}
	var v struct{ Email string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	u, err := a.authStore.GetUserByEmail(r.Context(), v.Email)
	if err == nil {
		token, hash, e := security.RandomToken()
		if e == nil {
			e = a.authStore.CreatePasswordReset(r.Context(), model.CreatePasswordResetParams{TokenHash: hash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
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

func (a *MongoAPI) recoverySession(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, "recovery") {
		return
	}
	var v struct{ Token string }
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	uid, err := a.authStore.UsePasswordReset(r.Context(), security.HashToken(v.Token))
	if err != nil {
		fail(w, 400, "invalid_token", "Link expired or already used")
		return
	}
	u, err := a.authStore.GetUserByID(r.Context(), uid)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if err := a.newSession(r.Context(), w, r, uid); err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"user": publicUser(u)})
}

func (a *MongoAPI) changePassword(w http.ResponseWriter, r *http.Request) {
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
		a.serverError(w, err)
		return
	}
	p := principal(r)
	err = a.database.InTx(r.Context(), func(tx context.Context) error {
		if err := a.authStore.SetUserPassword(tx, model.SetUserPasswordParams{ID: p.UserID, PasswordHash: hash}); err != nil {
			return err
		}
		if err := a.authStore.InvalidatePasswordResets(tx, p.UserID); err != nil {
			return err
		}
		return a.authStore.RevokeOtherSessions(tx, p.UserID, p.SessionID)
	})
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.audit(r, p.UserID, "account.change_password")
	respond(w, 200, map[string]any{"ok": true})
}

func (a *MongoAPI) me(w http.ResponseWriter, r *http.Request) {
	u, err := a.authStore.GetUserByID(r.Context(), principal(r).UserID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"user": publicUser(u)})
}

func (a *MongoAPI) updateMe(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Name                *string `json:"name"`
		Currency            *string `json:"currency"`
		Country             *string `json:"country"`
		Timezone            *string `json:"timezone"`
		Language            *string `json:"language"`
		ExpectedIncomeMinor *int64  `json:"expectedIncomeMinor"`
		WarnAtBp            *int32  `json:"warnAtBp"`
		AutoLockMinutes     *int32  `json:"autoLockMinutes"`
	}
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	u, err := a.authStore.GetUserByID(r.Context(), principal(r).UserID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if v.Name != nil {
		u.Name = strings.TrimSpace(*v.Name)
	}
	if v.Currency != nil {
		u.Currency = strings.ToUpper(*v.Currency)
	}
	if v.Country != nil {
		u.Country = strings.ToUpper(*v.Country)
	}
	if v.Timezone != nil {
		u.Timezone = *v.Timezone
	}
	if v.Language != nil {
		u.Language = *v.Language
	}
	if v.ExpectedIncomeMinor != nil {
		u.ExpectedIncomeMinor = *v.ExpectedIncomeMinor
	}
	if v.WarnAtBp != nil {
		u.WarnAtBp = *v.WarnAtBp
	}
	if v.AutoLockMinutes != nil {
		u.AutoLockMinutes = *v.AutoLockMinutes
	}
	if len(u.Name) > 100 || !money.ValidCurrency(strings.TrimSpace(u.Currency)) || len(strings.TrimSpace(u.Country)) != 2 || u.ExpectedIncomeMinor < 0 || u.WarnAtBp < 1000 || u.WarnAtBp > 10000 || u.AutoLockMinutes < 1 || u.AutoLockMinutes > 1440 {
		fail(w, 400, "validation_failed", "Invalid profile settings")
		return
	}
	if _, err := time.LoadLocation(u.Timezone); err != nil {
		fail(w, 400, "validation_failed", "Invalid timezone")
		return
	}
	u, err = a.authStore.UpdateUser(r.Context(), model.UpdateUserParams{ID: u.ID, Name: u.Name, Currency: u.Currency, Country: u.Country, Timezone: u.Timezone, Language: u.Language, ExpectedIncomeMinor: u.ExpectedIncomeMinor, WarnAtBp: u.WarnAtBp, AutoLockMinutes: u.AutoLockMinutes, LastBackupAt: u.LastBackupAt})
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"user": publicUser(u)})
}

func (a *MongoAPI) deleteMe(w http.ResponseWriter, r *http.Request) {
	var sub struct {
		Code  *string `bson:"paystack_subscription_code"`
		Token *string `bson:"paystack_email_token"`
	}
	err := a.database.DB.Collection("subscriptions").FindOne(r.Context(), bson.M{"_id": principal(r).UserID.String()}).Decode(&sub)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		a.serverError(w, err)
		return
	}
	if sub.Code != nil && *sub.Code != "" {
		if sub.Token == nil || *sub.Token == "" {
			fail(w, 409, "active_subscription", "Cancel your card subscription before deleting this account")
			return
		}
		if err := paystack.NewClient(a.cfg.PaystackSecretKey).Disable(r.Context(), *sub.Code, *sub.Token); err != nil {
			fail(w, 503, "billing_unavailable", "Could not cancel the card subscription; account was not deleted")
			return
		}
	}
	a.audit(r, principal(r).UserID, "account.delete")
	if err := a.authStore.DeleteUser(r.Context(), principal(r).UserID); err != nil {
		a.serverError(w, err)
		return
	}
	a.clearCookies(w)
	w.WriteHeader(204)
}

func (a *MongoAPI) getVault(w http.ResponseWriter, r *http.Request) {
	v, err := a.vaults.get(r.Context(), principal(r).UserID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		respond(w, 200, map[string]any{"vault": nil})
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"vault": v})
}

func (a *MongoAPI) createVault(w http.ResponseWriter, r *http.Request) {
	var input struct {
		vault
		Finance json.RawMessage `json:"finance"`
	}
	if err := decode(r, &input); err != nil {
		badJSON(w, err)
		return
	}
	v := input.vault
	if !validJSON(v.KDF) || !validJSON(v.WrappedKey) || !validJSON(v.RecoveryKey) || !validJSON(v.Data) || !validJSON(input.Finance) {
		fail(w, 400, "validation_failed", "Vault and finance fields are required")
		return
	}
	v, err := a.vaults.create(r.Context(), principal(r).UserID, v, input.Finance)
	if mongo.IsDuplicateKeyError(err) {
		fail(w, 409, "vault_exists", "Vault already exists")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"vault": v})
}

func (a *MongoAPI) saveVault(w http.ResponseWriter, r *http.Request) {
	var v struct {
		ExpectedVersion int64           `json:"expectedVersion"`
		Data            json.RawMessage `json:"data"`
		WrappedKey      json.RawMessage `json:"wrappedKey"`
		RecoveryKey     json.RawMessage `json:"recoveryKey"`
		Finance         json.RawMessage `json:"finance"`
	}
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	if v.ExpectedVersion < 1 || (len(v.Data) == 0 && len(v.WrappedKey) == 0 && len(v.RecoveryKey) == 0) || (len(v.Data) > 0 && (!validJSON(v.Data) || !validJSON(v.Finance))) || (len(v.WrappedKey) > 0 && !validJSON(v.WrappedKey)) || (len(v.RecoveryKey) > 0 && !validJSON(v.RecoveryKey)) {
		fail(w, 400, "validation_failed", "Invalid vault update")
		return
	}
	version, err := a.vaults.save(r.Context(), principal(r).UserID, v.ExpectedVersion, v.Data, v.WrappedKey, v.RecoveryKey, v.Finance)
	if errors.Is(err, mongo.ErrNoDocuments) {
		fail(w, 409, "version_conflict", "The data changed on another device")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"version": version})
}

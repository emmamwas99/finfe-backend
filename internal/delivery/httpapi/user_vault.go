package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"finfe/internal/domain/money"
	"finfe/internal/infrastructure/paystack"
	"finfe/internal/infrastructure/persistence/store"
)

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	u, err := a.q.GetUserByID(r.Context(), principal(r).UserID)
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"user": publicUser(u)})
}
func (a *API) updateMe(w http.ResponseWriter, r *http.Request) {
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
	u, err := a.q.GetUserByID(r.Context(), principal(r).UserID)
	if err != nil {
		serverError(a, w, err)
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
	if _, err = time.LoadLocation(u.Timezone); err != nil {
		fail(w, 400, "validation_failed", "Invalid timezone")
		return
	}
	u, err = a.q.UpdateUser(r.Context(), store.UpdateUserParams{ID: u.ID, Name: u.Name, Currency: u.Currency, Country: u.Country, Timezone: u.Timezone, Language: u.Language, ExpectedIncomeMinor: u.ExpectedIncomeMinor, WarnAtBp: u.WarnAtBp, AutoLockMinutes: u.AutoLockMinutes, LastBackupAt: u.LastBackupAt})
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"user": publicUser(u)})
}
func (a *API) deleteMe(w http.ResponseWriter, r *http.Request) {
	var code, token *string
	err := a.db.QueryRow(r.Context(), `SELECT paystack_subscription_code,paystack_email_token FROM subscriptions WHERE user_id=$1`, principal(r).UserID).Scan(&code, &token)
	if err != nil && err != pgx.ErrNoRows {
		serverError(a, w, err)
		return
	}
	if code != nil && *code != "" {
		if token == nil || *token == "" {
			fail(w, 409, "active_subscription", "Cancel your card subscription before deleting this account")
			return
		}
		if err = paystack.NewClient(a.cfg.PaystackSecretKey).Disable(r.Context(), *code, *token); err != nil {
			fail(w, 503, "billing_unavailable", "Could not cancel the card subscription; account was not deleted")
			return
		}
	}
	a.audit(r, principal(r).UserID, "account.delete")
	if err := a.q.DeleteUser(r.Context(), principal(r).UserID); err != nil {
		serverError(a, w, err)
		return
	}
	a.clearCookies(w)
	w.WriteHeader(204)
}

type vault struct {
	Version     int64           `json:"version"`
	KDF         json.RawMessage `json:"kdf"`
	WrappedKey  json.RawMessage `json:"wrappedKey"`
	RecoveryKey json.RawMessage `json:"recoveryKey"`
	Data        json.RawMessage `json:"data"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

func (a *API) getVault(w http.ResponseWriter, r *http.Request) {
	var v vault
	err := a.db.QueryRow(r.Context(), `SELECT version,kdf,wrapped_key,recovery_key,data,updated_at FROM vaults WHERE user_id=$1`, principal(r).UserID).Scan(&v.Version, &v.KDF, &v.WrappedKey, &v.RecoveryKey, &v.Data, &v.UpdatedAt)
	if err == pgx.ErrNoRows {
		respond(w, 200, map[string]any{"vault": nil})
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"vault": v})
}
func validJSON(x json.RawMessage) bool { return len(x) > 0 && json.Valid(x) && string(x) != "null" }
func (a *API) createVault(w http.ResponseWriter, r *http.Request) {
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
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(a, w, err)
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `INSERT INTO vaults(user_id,kdf,wrapped_key,recovery_key,data) VALUES($1,$2,$3,$4,$5) ON CONFLICT (user_id) DO NOTHING RETURNING version,updated_at`, principal(r).UserID, v.KDF, v.WrappedKey, v.RecoveryKey, v.Data).Scan(&v.Version, &v.UpdatedAt)
	if err == pgx.ErrNoRows {
		fail(w, 409, "vault_exists", "Vault already exists")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	if err = a.replaceSnapshot(r.Context(), tx, principal(r).UserID, input.Finance); err != nil {
		dbFailure(a, w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 201, map[string]any{"vault": v})
}
func (a *API) saveVault(w http.ResponseWriter, r *http.Request) {
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
	var version int64
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(a, w, err)
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `UPDATE vaults SET version=version+1, data=COALESCE($3::jsonb,data), wrapped_key=COALESCE($4::jsonb,wrapped_key), recovery_key=COALESCE($5::jsonb,recovery_key),updated_at=now() WHERE user_id=$1 AND version=$2 RETURNING version`, principal(r).UserID, v.ExpectedVersion, nullableJSON(v.Data), nullableJSON(v.WrappedKey), nullableJSON(v.RecoveryKey)).Scan(&version)
	if err == pgx.ErrNoRows {
		fail(w, 409, "version_conflict", "The data changed on another device")
		return
	}
	if err != nil {
		serverError(a, w, err)
		return
	}
	if len(v.Data) > 0 {
		if err = a.replaceSnapshot(r.Context(), tx, principal(r).UserID, v.Finance); err != nil {
			dbFailure(a, w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(a, w, err)
		return
	}
	respond(w, 200, map[string]any{"version": version})
}
func nullableJSON(x json.RawMessage) any {
	if len(x) == 0 {
		return nil
	}
	return x
}

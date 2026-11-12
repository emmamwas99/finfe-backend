package httpapi

import (
	"net"
	"net/http"
	"strings"
	"time"

	"finfe/internal/domain/model"
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

func publicUser(u model.User) map[string]any {
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

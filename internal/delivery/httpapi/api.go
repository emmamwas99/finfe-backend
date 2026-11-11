package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"finfe/internal/config"
	"finfe/internal/infrastructure/email"
	"finfe/internal/infrastructure/persistence/store"
	ratelimit "finfe/internal/infrastructure/redis"
	"finfe/internal/infrastructure/security"
)

type API struct {
	cfg     config.Config
	db      *pgxpool.Pool
	q       *store.Queries
	mail    email.Mailer
	log     *slog.Logger
	limiter ratelimit.Limiter
}

func New(cfg config.Config, db *pgxpool.Pool, mail email.Mailer, log *slog.Logger) *API {
	a := &API{cfg: cfg, db: db, q: store.New(db), mail: mail, log: log, limiter: ratelimit.NewMemoryLimiter()}
	if cfg.RedisURL != "" {
		if c, err := ratelimit.NewClient(context.Background(), cfg.RedisURL); err == nil {
			a.limiter = ratelimit.RedisLimiter{C: c}
		} else {
			log.Warn("redis unavailable; using process-local rate limits", "error", err)
		}
	}
	return a
}

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, a.logRequests, middleware.Recoverer)
	if a.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(a.cors, a.originGuard)
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/ready", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := a.db.Ping(ctx); err != nil {
			fail(w, http.StatusServiceUnavailable, "unavailable", "Database is unavailable")
			return
		}
		respond(w, http.StatusOK, map[string]string{"status": "ready"})
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

type principalKey struct{}

func principal(r *http.Request) security.Principal {
	return r.Context().Value(principalKey{}).(security.Principal)
}

func (a *API) auth(next http.Handler) http.Handler {
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
		s, err := a.q.GetActiveSession(r.Context(), p.SessionID)
		if err != nil || s.UserID != p.UserID {
			fail(w, 401, "unauthenticated", "Session expired")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

func (a *API) allowedOrigin(origin string) bool {
	for _, o := range a.cfg.CORSOrigins {
		if o == origin {
			return true
		}
	}
	return origin == a.cfg.AppURL
}

func (a *API) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && a.allowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			if origin != "" && a.allowedOrigin(origin) {
				w.WriteHeader(204)
			} else {
				w.WriteHeader(403)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Cookies are SameSite=Lax, and unsafe browser requests must come from the configured app.
// Requests without Origin are accepted only when a bearer token is supplied (non-browser clients).
func (a *API) originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && strings.HasPrefix(r.URL.Path, "/api/") {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if !a.allowedOrigin(origin) {
					fail(w, 403, "bad_origin", "Origin is not allowed")
					return
				}
			} else if r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") != "" {
				fail(w, 403, "bad_origin", "Origin is required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func decode(r *http.Request, out any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("Content-Type must be application/json")
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("only one JSON value is allowed")
	}
	return nil
}
func respond(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}
func fail(w http.ResponseWriter, code int, key, msg string) {
	respond(w, code, map[string]any{"error": map[string]string{"code": key, "message": msg}})
}
func badJSON(w http.ResponseWriter, err error) { fail(w, 400, "invalid_request", err.Error()) }
func serverError(a *API, w http.ResponseWriter, err error) {
	a.log.Error("request failed", "error", err)
	fail(w, 500, "internal_error", "Something went wrong")
}
func paramID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, 400, "invalid_id", "Invalid ID")
		return uuid.Nil, false
	}
	return id, true
}
func parseDate(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }
func absoluteLink(base, path, token string) string {
	u, _ := url.Parse(base)
	u.Path = path
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String()
}

func (a *API) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		a.log.Info("http request", "request_id", middleware.GetReqID(r.Context()), "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "duration_ms", time.Since(start).Milliseconds())
	})
}

func (a *API) audit(r *http.Request, uid uuid.UUID, action string) {
	if err := a.q.InsertAuditEvent(r.Context(), store.InsertAuditEventParams{UserID: &uid, Action: action, Ip: r.RemoteAddr, UserAgent: r.UserAgent(), Metadata: []byte("{}")}); err != nil {
		a.log.Error("audit event failed", "action", action, "error", err)
	}
}

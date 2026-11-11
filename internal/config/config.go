// Package config loads settings from the environment (12-factor). Unsafe production
// settings fail fast at startup.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env      string // development | test | production
	HTTPAddr string
	AppURL   string // the web app's public URL (email links, Paystack callback)

	DatabaseURL      string
	DatabaseMaxConns int32
	MigrateOnStart   bool
	RedisURL         string

	JWTSecret       []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	CookieDomain    string
	CookieSecure    bool
	CORSOrigins     []string
	// TrustProxy reads the client IP from X-Forwarded-For / X-Real-IP (only behind a trusted proxy).
	TrustProxy bool

	RequireEmailVerification bool
	TrialDays                int

	MailDriver   string // log | smtp
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	MailFrom     string

	PaystackSecretKey     string
	PaystackWebhookSecret string
	PaystackPlanMonthly   string
	PaystackPlanYearly    string
	BillingDemo           bool // simulated checkout for local development
}

func (c Config) Production() bool { return c.Env == "production" }

// Load reads the environment.
func Load() (Config, error) {
	env := get("APP_ENV", "development")
	prod := env == "production"
	c := Config{
		Env:                      env,
		HTTPAddr:                 get("HTTP_ADDR", ":8080"),
		AppURL:                   strings.TrimRight(get("APP_URL", "http://localhost:3000"), "/"),
		DatabaseURL:              get("DATABASE_URL", "postgres://finfe:finfe@localhost:5432/finfe?sslmode=disable"),
		DatabaseMaxConns:         int32(getInt("DATABASE_MAX_CONNS", 20)),
		MigrateOnStart:           getBool("MIGRATE_ON_START", !prod),
		RedisURL:                 get("REDIS_URL", ""),
		JWTSecret:                []byte(get("JWT_SECRET", "")),
		AccessTokenTTL:           getDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:          getDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		CookieDomain:             get("COOKIE_DOMAIN", ""),
		CookieSecure:             getBool("COOKIE_SECURE", prod),
		CORSOrigins:              splitList(get("CORS_ORIGINS", "http://localhost:3000")),
		TrustProxy:               getBool("TRUST_PROXY", false),
		RequireEmailVerification: getBool("REQUIRE_EMAIL_VERIFICATION", true),
		TrialDays:                getInt("TRIAL_DAYS", 14),
		MailDriver:               get("MAIL_DRIVER", "log"),
		SMTPHost:                 get("SMTP_HOST", ""),
		SMTPPort:                 getInt("SMTP_PORT", 587),
		SMTPUsername:             get("SMTP_USERNAME", ""),
		SMTPPassword:             get("SMTP_PASSWORD", ""),
		MailFrom:                 get("MAIL_FROM", "Finfe <no-reply@localhost>"),
		PaystackSecretKey:        get("PAYSTACK_SECRET_KEY", ""),
		PaystackWebhookSecret:    get("PAYSTACK_WEBHOOK_SECRET", ""),
		PaystackPlanMonthly:      get("PAYSTACK_PLAN_MONTHLY", ""),
		PaystackPlanYearly:       get("PAYSTACK_PLAN_YEARLY", ""),
	}
	c.BillingDemo = getBool("BILLING_DEMO", !prod && c.PaystackSecretKey == "")
	if len(c.JWTSecret) == 0 && !prod {
		c.JWTSecret = []byte("dev-only-insecure-secret-change-me-0123456789")
	}
	return c, c.validate()
}

func (c Config) validate() error {
	var errs []error
	if len(c.JWTSecret) < 32 {
		errs = append(errs, errors.New("JWT_SECRET must be at least 32 bytes"))
	}
	if c.Production() {
		if !c.CookieSecure {
			errs = append(errs, errors.New("COOKIE_SECURE must be true in production"))
		}
		if c.BillingDemo {
			errs = append(errs, errors.New("BILLING_DEMO cannot be enabled in production"))
		}
		if c.MailDriver != "smtp" {
			errs = append(errs, errors.New("MAIL_DRIVER must be smtp in production"))
		}
		if !strings.HasPrefix(c.AppURL, "https://") {
			errs = append(errs, errors.New("APP_URL must be https in production"))
		}
	}
	if c.MailDriver == "smtp" && c.SMTPHost == "" {
		errs = append(errs, errors.New("SMTP_HOST is required when MAIL_DRIVER=smtp"))
	}
	if c.AccessTokenTTL < time.Minute || c.RefreshTokenTTL < time.Hour {
		errs = append(errs, fmt.Errorf("token lifetimes are too short"))
	}
	return errors.Join(errs...)
}

func get(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func getInt(k string, def int) int {
	if v, err := strconv.Atoi(get(k, "")); err == nil {
		return v
	}
	return def
}

func getBool(k string, def bool) bool {
	if v, err := strconv.ParseBool(get(k, "")); err == nil {
		return v
	}
	return def
}

func getDuration(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(get(k, "")); err == nil {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

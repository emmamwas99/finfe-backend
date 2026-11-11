package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"finfe/internal/config"
)

func TestFinancialRoutesRequireAuthentication(t *testing.T) {
	a := New(config.Config{JWTSecret: []byte("test-secret-test-secret-test-secret"), AppURL: "http://localhost:3000"}, nil, nil, slog.Default())
	h := a.Router()
	for _, path := range []string{"/api/v1/expenses", "/api/v1/expenses/summary", "/api/v1/income", "/api/v1/budgets", "/api/v1/savings", "/api/v1/goals", "/api/v1/reports/monthly", "/api/v1/financial-health/overview"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s: got %d, want 401", path, w.Code)
			}
		})
	}
}

func TestMinorRejectsFractionalCents(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
		ok   bool
	}{{"0.10", 10, true}, {"1234.56", 123456, true}, {"-1.25", -125, true}, {"1.001", 0, false}, {"999999999999999999999", 0, false}} {
		got, err := minor(json.Number(tc.in))
		if (err == nil) != tc.ok || tc.ok && got != tc.want {
			t.Errorf("minor(%s) = %d, %v", tc.in, got, err)
		}
	}
}

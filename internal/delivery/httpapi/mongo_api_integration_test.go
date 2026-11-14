package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"

	"finfe/internal/config"
	"finfe/internal/infrastructure/email"
	"finfe/internal/infrastructure/mongodb"
	"finfe/internal/infrastructure/paystack"
)

func TestMongoAPIRegisterQueuesConfirmationIntegration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run the MongoDB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := mongodb.Open(ctx, uri, "finfe_test_"+uuid.NewString()[:12], 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(context.Background()); _ = db.Close(context.Background()) })
	if err := db.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	transport := &email.Log{}
	mailQueue := email.NewOutbox(db.DB, transport, []byte("test-secret-test-secret-test-secret"), slog.Default())
	cfg := config.Config{JWTSecret: []byte("test-secret-test-secret-test-secret"), AppURL: "http://localhost:3000", CORSOrigins: []string{"http://localhost:3000"}, AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 24 * time.Hour, TrialDays: 14, RequireEmailVerification: true}
	h := NewMongo(cfg, db, mailQueue, slog.Default()).Router()
	address := "queue-" + uuid.NewString() + "@example.invalid"
	payload, _ := json.Marshal(map[string]string{"email": address, "password": "long-test-password", "name": "Queue"})
	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(w, req)
	t.Logf("registration response time with queued email: %s", time.Since(start))
	if w.Code != 201 || len(transport.Sent()) != 0 {
		t.Fatalf("registration should return before delivery: status=%d mails=%d body=%s", w.Code, len(transport.Sent()), w.Body.String())
	}
	count, err := db.DB.Collection("mail_outbox").CountDocuments(ctx, bson.M{})
	if err != nil || count != 1 {
		t.Fatalf("confirmation was not queued: count=%d error=%v", count, err)
	}
	go mailQueue.Run(ctx)
	deadline := time.After(10 * time.Second)
	for len(transport.Sent()) == 0 {
		select {
		case <-deadline:
			t.Fatal("queued confirmation was not delivered")
		case <-time.After(50 * time.Millisecond):
		}
	}
	if transport.Sent()[0].To != address {
		t.Fatal("queued confirmation went to wrong address")
	}
	for {
		count, err = db.DB.Collection("mail_outbox").CountDocuments(ctx, bson.M{})
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("delivered confirmation was not removed from queue")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestMongoAPIResendVerificationIntegration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run the MongoDB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := mongodb.Open(ctx, uri, "finfe_test_"+uuid.NewString()[:12], 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(context.Background()); _ = db.Close(context.Background()) })
	if err := db.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	mailer := &email.Log{}
	cfg := config.Config{JWTSecret: []byte("test-secret-test-secret-test-secret"), AppURL: "http://localhost:3000", CORSOrigins: []string{"http://localhost:3000"}, AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 24 * time.Hour, TrialDays: 14, RequireEmailVerification: true}
	h := NewMongo(cfg, db, mailer, slog.Default()).Router()
	request := func(path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		payload, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost:3000")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	address := "verify-" + uuid.NewString() + "@example.invalid"
	w := request("/api/v1/auth/register", map[string]any{"email": address, "password": "long-test-password", "name": "Verify"})
	if w.Code != 201 || !bytes.Contains(w.Body.Bytes(), []byte(`"needsConfirmation":true`)) || len(mailer.Sent()) != 1 {
		t.Fatalf("registration: status=%d body=%s mails=%d", w.Code, w.Body.String(), len(mailer.Sent()))
	}
	w = request("/api/v1/auth/login", map[string]any{"email": address, "password": "long-test-password"})
	if w.Code != 403 || !bytes.Contains(w.Body.Bytes(), []byte(`"email_unverified"`)) {
		t.Fatalf("unverified login: %d %s", w.Code, w.Body.String())
	}
	w = request("/api/v1/auth/resend-verification", map[string]any{"email": "unknown@example.invalid"})
	if w.Code != 200 || len(mailer.Sent()) != 1 {
		t.Fatalf("unknown account resend: status=%d mails=%d", w.Code, len(mailer.Sent()))
	}
	w = request("/api/v1/auth/resend-verification", map[string]any{"email": address})
	if w.Code != 200 || len(mailer.Sent()) != 2 {
		t.Fatalf("existing account resend: status=%d mails=%d", w.Code, len(mailer.Sent()))
	}
	parts := strings.SplitN(mailer.Sent()[1].Body, "?token=", 2)
	if len(parts) != 2 {
		t.Fatal("resend message has no confirmation token")
	}
	token := strings.SplitN(parts[1], "\n", 2)[0]
	w = request("/api/v1/auth/verify-email", map[string]any{"token": token})
	if w.Code != 200 {
		t.Fatalf("verify: %d %s", w.Code, w.Body.String())
	}
	w = request("/api/v1/auth/login", map[string]any{"email": address, "password": "long-test-password"})
	if w.Code != 200 {
		t.Fatalf("verified login: %d %s", w.Code, w.Body.String())
	}
	w = request("/api/v1/auth/resend-verification", map[string]any{"email": address})
	if w.Code != 200 || len(mailer.Sent()) != 2 {
		t.Fatalf("verified account resend: status=%d mails=%d", w.Code, len(mailer.Sent()))
	}
}

func TestMongoAPIAuthAndVaultIntegration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run the MongoDB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := mongodb.Open(ctx, uri, "finfe_test_"+uuid.NewString()[:12], 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(context.Background()); _ = db.Close(context.Background()) })
	if err := db.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: []byte("test-secret-test-secret-test-secret"), AppURL: "http://localhost:3000", CORSOrigins: []string{"http://localhost:3000"}, AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 24 * time.Hour, TrialDays: 14, BillingDemo: true, PaystackSecretKey: "sk_test_mongo_integration"}
	a := NewMongo(cfg, db, &email.Log{Logger: slog.Default()}, slog.Default())
	h := a.Router()
	request := func(method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Origin", "http://localhost:3000")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	w := request("POST", "/api/v1/auth/register", map[string]any{"email": "mongo@example.com", "password": "long-test-password", "name": "Mongo"})
	if w.Code != 201 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) < 2 {
		t.Fatalf("missing auth cookies: %v", cookies)
	}
	w = request("POST", "/api/v1/auth/refresh", nil, cookies...)
	if w.Code != 200 {
		t.Fatalf("refresh: %d %s", w.Code, w.Body.String())
	}
	rotated := w.Result().Cookies()
	if len(rotated) < 2 {
		t.Fatalf("missing rotated cookies: %v", rotated)
	}
	w = request("POST", "/api/v1/auth/refresh", nil, cookies...)
	if w.Code != 401 || !bytes.Contains(w.Body.Bytes(), []byte(`"token_reused"`)) {
		t.Fatalf("refresh replay: %d %s", w.Code, w.Body.String())
	}
	cookies = rotated
	w = request("GET", "/api/v1/users/me", nil, cookies...)
	if w.Code != 200 {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	finance := map[string]any{"settings": map[string]any{"name": "Mongo", "expectedIncome": 1000, "warnAt": 0.8, "autoLockMinutes": 15}, "categories": []any{}, "expenses": []any{}}
	w = request("POST", "/api/v1/vault", map[string]any{"kdf": map[string]any{"alg": "test"}, "wrappedKey": map[string]any{"key": "test"}, "recoveryKey": map[string]any{"key": "backup"}, "data": map[string]any{"ciphertext": "one"}, "finance": finance}, cookies...)
	if w.Code != 201 {
		t.Fatalf("create vault: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/vault", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"version":1`)) {
		t.Fatalf("get vault: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/categories/", map[string]any{"name": "Transport", "kind": "need"}, cookies...)
	if w.Code != 201 {
		t.Fatalf("create category: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Data.ID == "" {
		t.Fatalf("category response: %s %v", w.Body.String(), err)
	}
	w = request("POST", "/api/v1/recurring/", map[string]any{"kind": "expense", "name": "Rent", "amount_minor": 5000, "currency": "KES", "category_id": created.Data.ID, "frequency": "monthly", "start_date": "2026-11-01"}, cookies...)
	if w.Code != 400 {
		t.Fatalf("invalid recurring rule: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/expenses/", map[string]any{"occurred_on": "2026-11-12", "amount_minor": 250, "currency": "KES", "category_id": created.Data.ID}, cookies...)
	if w.Code != 201 {
		t.Fatalf("create expense: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/expenses/?from=2026-11-01&to=2026-11-30", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"total":1`)) {
		t.Fatalf("list expenses: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/expenses/summary?month=2026-11", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"total_minor":250`)) {
		t.Fatalf("expense summary: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/reports/monthly?month=2026-11", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"expenses_minor":250`)) {
		t.Fatalf("monthly report: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/savings/", map[string]any{"name": "Emergency", "currency": "KES", "is_emergency_fund": true}, cookies...)
	if w.Code != 201 {
		t.Fatalf("create savings: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	accountID := created.Data.ID
	w = request("POST", "/api/v1/savings/"+accountID+"/transactions", map[string]any{"kind": "deposit", "amount_minor": 1000, "occurred_on": "2026-11-12"}, cookies...)
	if w.Code != 201 {
		t.Fatalf("deposit: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/savings/"+accountID+"/transactions", map[string]any{"kind": "withdrawal", "amount_minor": 1200, "occurred_on": "2026-11-12"}, cookies...)
	if w.Code != 409 {
		t.Fatalf("overdraw: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/savings/"+accountID+"/transactions", map[string]any{"kind": "withdrawal", "amount_minor": 500, "occurred_on": "2026-11-12"}, cookies...)
	if w.Code != 201 {
		t.Fatalf("withdrawal: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/savings/"+accountID+"/progress", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"balance_minor":500`)) {
		t.Fatalf("savings progress: %d %s", w.Code, w.Body.String())
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v1/savings/"+accountID+"/transactions", bytes.NewBufferString(`{"kind":"withdrawal","amount_minor":400,"occurred_on":"2026-11-12"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "http://localhost:3000")
			for _, c := range cookies {
				req.AddCookie(c)
			}
			writer := httptest.NewRecorder()
			h.ServeHTTP(writer, req)
			statuses <- writer.Code
		}()
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for code := range statuses {
		counts[code]++
	}
	if counts[201] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent withdrawals: %#v", counts)
	}
	w = request("GET", "/api/v1/savings/"+accountID+"/progress", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"balance_minor":100`)) {
		t.Fatalf("concurrent savings balance: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/billing/checkout", map[string]any{"period": "monthly", "method": "mpesa"}, cookies...)
	if w.Code != 200 {
		t.Fatalf("billing checkout: %d %s", w.Code, w.Body.String())
	}
	var checkout struct {
		Reference string `json:"reference"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &checkout); err != nil || checkout.Reference == "" {
		t.Fatalf("checkout response: %s %v", w.Body.String(), err)
	}
	w = request("POST", "/api/v1/billing/demo/"+checkout.Reference, map[string]any{"paid": true}, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"status":"success"`)) {
		t.Fatalf("demo payment: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/billing/subscription", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"status":"active"`)) {
		t.Fatalf("subscription: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/reports/savings-trends?month=2026-11", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"withdrawals_minor":900`)) {
		t.Fatalf("savings trends: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/financial-health/trends?month=2026-11", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"spent_minor":250`)) {
		t.Fatalf("health trends: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/financial-health/overview", nil, cookies...)
	if w.Code != 200 {
		t.Fatalf("financial health: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/billing/checkout", map[string]any{"period": "monthly", "method": "card"}, cookies...)
	if w.Code != 200 {
		t.Fatalf("webhook checkout: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &checkout); err != nil {
		t.Fatal(err)
	}
	webhookBody, _ := json.Marshal(map[string]any{"event": "charge.success", "data": map[string]any{"status": "success", "reference": checkout.Reference, "amount": paystack.PriceMinor[paystack.Monthly], "currency": "KES", "channel": "card", "customer": map[string]any{"customer_code": "CUS_TEST"}, "metadata": `{"period":"monthly"}`}})
	mac := hmac.New(sha512.New, []byte(cfg.PaystackSecretKey))
	_, _ = mac.Write(webhookBody)
	webhookReq := httptest.NewRequest("POST", "/api/v1/billing/webhook", bytes.NewReader(webhookBody))
	webhookReq.Header.Set("X-Paystack-Signature", hex.EncodeToString(mac.Sum(nil)))
	webhookReq.Header.Set("Origin", "http://localhost:3000")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, webhookReq)
	if w.Code != 200 {
		t.Fatalf("webhook: %d %s", w.Code, w.Body.String())
	}
	payment, err := a.payment(ctx, checkout.Reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := uuid.Parse(stringField(payment, "user_id"))
	if err != nil {
		t.Fatal(err)
	}
	subBeforeRetry, err := a.subscription(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	retry := httptest.NewRequest("POST", "/api/v1/billing/webhook", bytes.NewReader(webhookBody))
	retry.Header.Set("X-Paystack-Signature", hex.EncodeToString(mac.Sum(nil)))
	retry.Header.Set("Origin", "http://localhost:3000")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, retry)
	if w.Code != 200 {
		t.Fatalf("webhook retry: %d %s", w.Code, w.Body.String())
	}
	subAfterRetry, err := a.subscription(ctx, userID)
	if err != nil || subBeforeRetry["currentPeriodEnd"] != subAfterRetry["currentPeriodEnd"] {
		t.Fatalf("webhook retry extended subscription: before=%v after=%v err=%v", subBeforeRetry, subAfterRetry, err)
	}
	w = request("POST", "/api/v1/billing/verify", map[string]any{"reference": checkout.Reference}, cookies...)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"status":"success"`)) {
		t.Fatalf("webhook payment: %d %s", w.Code, w.Body.String())
	}
}

package security

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var fast = Argon2Params{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse battery", DefaultArgon2)
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q err %v", h, err)
	}
	if rehash, err := VerifyPassword("correct horse battery", h); err != nil || rehash {
		t.Fatalf("verify: rehash=%v err=%v", rehash, err)
	}
	if _, err := VerifyPassword("wrong", h); err != ErrMismatch {
		t.Fatalf("expected mismatch, got %v", err)
	}
	h2, _ := HashPassword("correct horse battery", DefaultArgon2)
	if h == h2 {
		t.Fatal("salts must differ")
	}
	weak, _ := HashPassword("x", fast)
	if rehash, err := VerifyPassword("x", weak); err != nil || !rehash {
		t.Fatal("weaker parameters should ask for a rehash")
	}
	if _, err := VerifyPassword("x", "$bcrypt$nope"); err == nil {
		t.Fatal("unsupported format must fail")
	}
}

func TestAccessTokens(t *testing.T) {
	secret := []byte(strings.Repeat("s", 32))
	p := Principal{UserID: uuid.New(), SessionID: uuid.New()}
	now := time.Now()
	tok, exp, err := IssueAccessToken(secret, p, 15*time.Minute, now)
	if err != nil || exp.Sub(now) != 15*time.Minute {
		t.Fatal(err)
	}
	got, err := ParseAccessToken(secret, tok)
	if err != nil || got != p {
		t.Fatalf("got %+v err %v", got, err)
	}
	if _, err := ParseAccessToken([]byte(strings.Repeat("x", 32)), tok); err == nil {
		t.Fatal("wrong secret must fail")
	}
	old, _, _ := IssueAccessToken(secret, p, time.Minute, now.Add(-time.Hour))
	if _, err := ParseAccessToken(secret, old); err == nil {
		t.Fatal("expired token must fail")
	}
	// "none" algorithm is rejected
	none := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{SessionID: p.SessionID, RegisteredClaims: jwt.RegisteredClaims{Subject: p.UserID.String(), Issuer: issuer, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}})
	s, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := ParseAccessToken(secret, s); err == nil {
		t.Fatal("alg=none must be rejected")
	}
}

func TestRandomTokens(t *testing.T) {
	a, ha, _ := RandomToken()
	b, _, _ := RandomToken()
	if a == b || len(a) < 40 || string(HashToken(a)) != string(ha) {
		t.Fatal("tokens must be random and hash consistently")
	}
}

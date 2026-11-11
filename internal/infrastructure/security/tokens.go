package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// RandomToken returns a URL-safe random token (32 bytes of entropy) and its SHA-256 hash.
// Only the hash is stored; the token goes to the client (cookie or email link).
func RandomToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken is the stored form of a random token.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Claims of an access token.
type Claims struct {
	SessionID uuid.UUID `json:"sid"`
	jwt.RegisteredClaims
}

// Principal is the authenticated caller.
type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

const issuer = "finfe-api"

// IssueAccessToken signs a short-lived HS256 JWT.
func IssueAccessToken(secret []byte, p Principal, ttl time.Duration, now time.Time) (string, time.Time, error) {
	exp := now.Add(ttl)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		SessionID: p.SessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: p.UserID.String(), Issuer: issuer, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(exp),
			ID: uuid.NewString(),
		},
	})
	s, err := tok.SignedString(secret)
	return s, exp, err
}

var ErrInvalidToken = errors.New("invalid or expired token")

// ParseAccessToken validates signature, algorithm, issuer and expiry.
func ParseAccessToken(secret []byte, token string) (Principal, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(issuer), jwt.WithExpirationRequired(), jwt.WithLeeway(5*time.Second))
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	uid, err := uuid.Parse(c.Subject)
	if err != nil || c.SessionID == uuid.Nil {
		return Principal{}, ErrInvalidToken
	}
	return Principal{UserID: uid, SessionID: c.SessionID}, nil
}

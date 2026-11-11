// Package security implements password hashing (Argon2id), tokens and CSRF helpers.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2Params follow OWASP's recommended minimum for Argon2id (19 MiB, 2 iterations, 1 lane).
type Argon2Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

var DefaultArgon2 = Argon2Params{MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32}

var ErrMismatch = errors.New("password does not match")

// HashPassword returns a PHC-format string: $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>.
func HashPassword(password string, p Argon2Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Iterations, p.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a PHC string in constant time.
// needsRehash is true when the stored parameters are weaker than the current defaults.
func VerifyPassword(password, encoded string) (needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Iterations, &p.Parallelism); err != nil {
		return false, errors.New("bad argon2 parameters")
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, ErrMismatch
	}
	d := DefaultArgon2
	return p.MemoryKiB < d.MemoryKiB || p.Iterations < d.Iterations || p.Parallelism < d.Parallelism, nil
}

// dummyHash lets login spend the same time when the email doesn't exist (no user enumeration by timing).
var dummyHash, _ = HashPassword("timing-equaliser", DefaultArgon2)

// BurnPasswordCheck performs a throwaway verification.
func BurnPasswordCheck(password string) { _, _ = VerifyPassword(password, dummyHash) }

package mongodb

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"finfe/internal/domain/model"
)

func TestAuthStoreIntegration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run the MongoDB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, uri, "finfe_test_"+uuid.NewString(), 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.DB.Drop(context.Background())
		_ = db.Close(context.Background())
	})
	if err := db.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	s := AuthStore{DB: db.DB}
	uid := uuid.New()
	u, err := s.CreateUser(ctx, model.CreateUserParams{ID: uid, Email: "TEST@Example.com", PasswordHash: "hash", Name: "Test", Currency: "KES", Country: "KE", Timezone: "Africa/Nairobi", Language: "en"})
	if err != nil || u.Email != "test@example.com" || u.WarnAtBp != 8000 {
		t.Fatalf("create user: %#v %v", u, err)
	}
	if _, err := s.CreateUser(ctx, model.CreateUserParams{ID: uuid.New(), Email: "test@example.com"}); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("case-insensitive unique email: %v", err)
	}
	lookedUp, err := s.GetUserByEmail(ctx, "TEST@example.COM")
	if err != nil || lookedUp.ID != uid {
		t.Fatalf("email lookup: %#v %v", lookedUp, err)
	}
	hash := []byte("verification-hash")
	if err := s.CreateEmailVerification(ctx, model.CreateEmailVerificationParams{TokenHash: hash, UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	got, err := s.UseEmailVerification(ctx, hash)
	if err != nil || got != uid {
		t.Fatalf("verify token: %s %v", got, err)
	}
	if _, err := s.UseEmailVerification(ctx, hash); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("verification replay: %v", err)
	}
	sid := uuid.New()
	if _, err := s.CreateSession(ctx, model.CreateSessionParams{ID: sid, UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	refreshID := uuid.New()
	refreshHash := []byte("refresh-hash")
	if err := s.CreateRefreshToken(ctx, model.CreateRefreshTokenParams{ID: refreshID, SessionID: sid, TokenHash: refreshHash, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetRefreshToken(ctx, refreshHash)
	if err != nil || row.UserID != uid || row.SessionID != sid {
		t.Fatalf("refresh lookup: %#v %v", row, err)
	}
	if _, err := s.UseRefreshToken(ctx, refreshID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UseRefreshToken(ctx, refreshID); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("refresh replay: %v", err)
	}
	if err := s.DeleteUser(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUserByID(ctx, uid); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("deleted user lookup: %v", err)
	}
	if _, err := s.GetRefreshToken(ctx, refreshHash); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("deleted refresh token lookup: %v", err)
	}
}

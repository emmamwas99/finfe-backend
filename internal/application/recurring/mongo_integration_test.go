package recurring

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"

	"finfe/internal/domain/model"
	"finfe/internal/infrastructure/mongodb"
)

func TestRunMongoIdempotentIntegration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run the MongoDB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := mongodb.Open(ctx, uri, "finfe_test_"+uuid.NewString(), 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(context.Background()); _ = db.Close(context.Background()) })
	if err := db.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	uid := uuid.New()
	if _, err := (mongodb.AuthStore{DB: db.DB}).CreateUser(ctx, model.CreateUserParams{ID: uid, Email: "recurring@example.com", PasswordHash: "hash", Name: "Recurring", Currency: "KES", Country: "KE", Timezone: "Africa/Nairobi", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	rs := mongodb.ResourceStore{DB: db.DB}
	category, err := rs.Create(ctx, "categories", uid, bson.M{"name": "Rent", "kind": "need"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rs.Create(ctx, "recurring_transactions", uid, bson.M{"kind": "expense", "name": "Rent", "amount_minor": int64(5000), "currency": "KES", "category_id": category["id"], "frequency": "monthly", "day_of_month": int64(12), "start_date": "2026-11-01"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	if err := RunMongo(ctx, db, now); err != nil {
		t.Fatal(err)
	}
	if err := RunMongo(ctx, db, now); err != nil {
		t.Fatal(err)
	}
	n, err := db.DB.Collection("expenses").CountDocuments(ctx, bson.M{"user_id": uid.String(), "occurred_on": "2026-11-12"})
	if err != nil || n != 1 {
		t.Fatalf("occurrences = %d, err %v", n, err)
	}
}

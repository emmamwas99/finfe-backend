package mongodb

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestResourceStoreIntegration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run the MongoDB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := "finfe_test_" + uuid.NewString()
	db, err := Open(ctx, uri, name, 5)
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
	s := ResourceStore{DB: db.DB}
	owner := uuid.New()
	other := uuid.New()
	category, err := s.Create(ctx, "categories", owner, bson.M{"name": "Food", "kind": "need"})
	if err != nil {
		t.Fatal(err)
	}
	if category["emoji"] != "•" || category["archived"] != false {
		t.Fatalf("category defaults missing: %#v", category)
	}
	categoryID := uuid.MustParse(category["id"].(string))
	if _, err := s.Create(ctx, "expenses", other, bson.M{"occurred_on": "2026-11-12", "amount_minor": int64(100), "currency": "KES", "category_id": categoryID.String()}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("cross-owner reference: got %v", err)
	}
	expense, err := s.Create(ctx, "expenses", owner, bson.M{"occurred_on": "2026-11-12", "amount_minor": int64(100), "currency": "KES", "category_id": categoryID.String()})
	if err != nil {
		t.Fatal(err)
	}
	expenseID := uuid.MustParse(expense["id"].(string))
	if _, err := s.Get(ctx, "expenses", other, expenseID); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("cross-owner read: got %v", err)
	}
	if _, err := s.Update(ctx, "expenses", other, expenseID, bson.M{"note": "bad"}); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("cross-owner update: got %v", err)
	}
	data, total, err := s.List(ctx, "expenses", owner, ResourceFilter{From: "2026-11-01", To: "2026-11-30"}, 1, 25)
	if err != nil || total != 1 || len(data) != 1 {
		t.Fatalf("list: total=%d rows=%d err=%v", total, len(data), err)
	}
	if err := s.Delete(ctx, "categories", owner, categoryID); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete used category: got %v", err)
	}
	if err := s.Delete(ctx, "expenses", other, expenseID); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("cross-owner delete: got %v", err)
	}
	if err := s.Delete(ctx, "expenses", owner, expenseID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "categories", owner, categoryID); err != nil {
		t.Fatal(err)
	}
}

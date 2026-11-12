package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"finfe/internal/domain/model"
	"finfe/internal/infrastructure/mongodb"
)

func TestMongoVaultTransactionIntegration(t *testing.T) {
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
	t.Cleanup(func() {
		_ = db.DB.Drop(context.Background())
		_ = db.Close(context.Background())
	})
	if err := db.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	uid := uuid.New()
	_, err = (mongodb.AuthStore{DB: db.DB}).CreateUser(ctx, model.CreateUserParams{ID: uid, Email: "vault@example.com", PasswordHash: "hash", Name: "Vault", Currency: "KES", Country: "KE", Timezone: "Africa/Nairobi", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	categoryID, expenseID := uuid.NewString(), uuid.NewString()
	finance := func(amount string) json.RawMessage {
		return json.RawMessage(`{"settings":{"name":"Vault","expectedIncome":1000,"warnAt":0.8,"autoLockMinutes":15},"categories":[{"id":"` + categoryID + `","name":"Food","emoji":"🍽️","kind":"need","budget":100}],"expenses":[{"id":"` + expenseID + `","date":"2026-11-12","amount":` + amount + `,"categoryId":"` + categoryID + `"}]}`)
	}
	s := mongoVaultStore{db: db.DB}
	v, err := s.create(ctx, uid, vault{KDF: json.RawMessage(`{"alg":"test"}`), WrappedKey: json.RawMessage(`{"key":"test"}`), RecoveryKey: json.RawMessage(`{"key":"backup"}`), Data: json.RawMessage(`{"ciphertext":"one"}`)}, finance("10"))
	if err != nil || v.Version != 1 {
		t.Fatalf("create vault: %#v %v", v, err)
	}
	stored, err := s.get(ctx, uid)
	if err != nil || stored.Version != 1 || string(stored.Data) != `{"ciphertext":"one"}` {
		t.Fatalf("read vault: %#v %v", stored, err)
	}
	var expense struct {
		AmountMinor int64 `bson:"amount_minor"`
	}
	if err := db.DB.Collection("expenses").FindOne(ctx, bson.M{"_id": expenseID}).Decode(&expense); err != nil || expense.AmountMinor != 1000 {
		t.Fatalf("initial projection: %#v %v", expense, err)
	}
	version, err := s.save(ctx, uid, 1, json.RawMessage(`{"ciphertext":"two"}`), nil, nil, finance("20"))
	if err != nil || version != 2 {
		t.Fatalf("save vault: %d %v", version, err)
	}
	if err := db.DB.Collection("expenses").FindOne(ctx, bson.M{"_id": expenseID}).Decode(&expense); err != nil || expense.AmountMinor != 2000 {
		t.Fatalf("updated projection: %#v %v", expense, err)
	}
	if _, err := s.save(ctx, uid, 1, json.RawMessage(`{"ciphertext":"stale"}`), nil, nil, finance("30")); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("stale version: %v", err)
	}
	if _, err := s.save(ctx, uid, 2, json.RawMessage(`{"ciphertext":"invalid"}`), nil, nil, finance("1.001")); err == nil {
		t.Fatal("invalid finance snapshot was accepted")
	}
	stored, err = s.get(ctx, uid)
	if err != nil || stored.Version != 2 || string(stored.Data) != `{"ciphertext":"two"}` {
		t.Fatalf("rollback of invalid snapshot: %#v %v", stored, err)
	}
	apiExpense, err := (mongodb.ResourceStore{DB: db.DB}).Create(ctx, "expenses", uid, bson.M{"occurred_on": "2026-11-12", "amount_minor": int64(100), "currency": "KES", "category_id": categoryID})
	if err != nil {
		t.Fatal(err)
	}
	emptyFinance := json.RawMessage(`{"settings":{"name":"Vault","expectedIncome":1000,"warnAt":0.8,"autoLockMinutes":15},"categories":[],"expenses":[]}`)
	if _, err := s.save(ctx, uid, 2, json.RawMessage(`{"ciphertext":"three"}`), nil, nil, emptyFinance); !errors.Is(err, mongodb.ErrInUse) {
		t.Fatalf("remove referenced category: %v", err)
	}
	stored, err = s.get(ctx, uid)
	if err != nil || stored.Version != 2 {
		t.Fatalf("reference rollback: %#v %v", stored, err)
	}
	if err := (mongodb.ResourceStore{DB: db.DB}).Delete(ctx, "expenses", uid, uuid.MustParse(apiExpense["id"].(string))); err != nil {
		t.Fatal(err)
	}
	if version, err := s.save(ctx, uid, 2, json.RawMessage(`{"ciphertext":"three"}`), nil, nil, emptyFinance); err != nil || version != 3 {
		t.Fatalf("remove unreferenced category: %d %v", version, err)
	}
}

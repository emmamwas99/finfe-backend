package email

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type failOnceMailer struct {
	attempts int
	sent     []Message
}

func (f *failOnceMailer) Send(_ context.Context, m Message) error {
	f.attempts++
	if f.attempts == 1 {
		return errors.New("temporary SMTP failure")
	}
	f.sent = append(f.sent, m)
	return nil
}

func TestOutboxQueuesEncryptedEmailAndDelivers(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("set MONGODB_TEST_URI to run the MongoDB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("finfe_test_" + uuid.NewString()[:12])
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	transport := &Log{}
	queue := NewOutbox(db, transport, []byte("test-outbox-secret"), slog.Default())
	m := Verification("http://localhost:3000", "person@example.invalid", "Person", "sensitive-test-token")
	if err := queue.Send(ctx, m); err != nil {
		t.Fatal(err)
	}
	if len(transport.Sent()) != 0 {
		t.Fatal("message was delivered before the worker ran")
	}
	var stored struct {
		Payload []byte `bson:"payload"`
	}
	if err := db.Collection("mail_outbox").FindOne(ctx, bson.M{}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored.Payload, []byte("sensitive-test-token")) || bytes.Contains(stored.Payload, []byte("person@example.invalid")) {
		t.Fatal("outbox stored email contents in plaintext")
	}
	processed, err := queue.processNext(ctx)
	if err != nil || !processed {
		t.Fatalf("process queued email: processed=%v error=%v", processed, err)
	}
	if sent := transport.Sent(); len(sent) != 1 || sent[0] != m {
		t.Fatalf("unexpected delivered messages: %d", len(sent))
	}
	count, err := db.Collection("mail_outbox").CountDocuments(ctx, bson.M{})
	if err != nil || count != 0 {
		t.Fatalf("sent email remains queued: count=%d error=%v", count, err)
	}
	flaky := &failOnceMailer{}
	retryQueue := NewOutbox(db, flaky, []byte("test-outbox-secret"), slog.Default())
	if err := retryQueue.Send(ctx, m); err != nil {
		t.Fatal(err)
	}
	processed, err = retryQueue.processNext(ctx)
	if err != nil || !processed || flaky.attempts != 1 {
		t.Fatalf("first delivery attempt: processed=%v attempts=%d error=%v", processed, flaky.attempts, err)
	}
	if _, err := db.Collection("mail_outbox").UpdateOne(ctx, bson.M{}, bson.M{"$set": bson.M{"next_attempt_at": time.Now().Add(-time.Second)}}); err != nil {
		t.Fatal(err)
	}
	processed, err = retryQueue.processNext(ctx)
	if err != nil || !processed || flaky.attempts != 2 || len(flaky.sent) != 1 {
		t.Fatalf("retried delivery: processed=%v attempts=%d sent=%d error=%v", processed, flaky.attempts, len(flaky.sent), err)
	}
	count, err = db.Collection("mail_outbox").CountDocuments(ctx, bson.M{})
	if err != nil || count != 0 {
		t.Fatalf("retried email remains queued: count=%d error=%v", count, err)
	}
}

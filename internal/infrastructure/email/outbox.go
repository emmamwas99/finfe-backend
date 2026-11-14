package email

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Outbox stores encrypted messages before returning to the HTTP handler.
// Run delivers them independently and retries transient SMTP failures.
type Outbox struct {
	collection *mongo.Collection
	transport  Mailer
	key        [32]byte
	log        *slog.Logger
	wake       chan struct{}
}

func NewOutbox(db *mongo.Database, transport Mailer, secret []byte, log *slog.Logger) *Outbox {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("finfe-mail-outbox-v1"))
	var key [32]byte
	copy(key[:], mac.Sum(nil))
	return &Outbox{collection: db.Collection("mail_outbox"), transport: transport, key: key, log: log, wake: make(chan struct{}, 1)}
}

func (o *Outbox) Send(ctx context.Context, m Message) error {
	if _, err := mail.ParseAddress(m.To); err != nil {
		return fmt.Errorf("invalid mail recipient: %w", err)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("invalid mail subject")
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(o.key[:])
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	payload := aead.Seal(nonce, nonce, plain, nil)
	now := time.Now().UTC()
	_, err = o.collection.InsertOne(ctx, bson.M{
		"_id": uuid.NewString(), "payload": payload, "status": "pending", "attempts": 0,
		"next_attempt_at": now, "created_at": now,
	})
	if err != nil {
		return err
	}
	select {
	case o.wake <- struct{}{}:
	default:
	}
	return nil
}

type outboxRecord struct {
	ID       string `bson:"_id"`
	Payload  []byte `bson:"payload"`
	Attempts int    `bson:"attempts"`
}

func (o *Outbox) decode(payload []byte) (Message, error) {
	block, err := aes.NewCipher(o.key[:])
	if err != nil {
		return Message{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Message{}, err
	}
	if len(payload) < aead.NonceSize() {
		return Message{}, errors.New("mail payload is too short")
	}
	nonce := payload[:aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, payload[aead.NonceSize():], nil)
	if err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(plain, &m); err != nil {
		return Message{}, err
	}
	return m, nil
}

func (o *Outbox) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := o.processNext(ctx)
		if err != nil && ctx.Err() == nil {
			o.log.Error("mail outbox poll failed", "error", err)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-o.wake:
		case <-ticker.C:
		}
	}
}

func (o *Outbox) processNext(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	filter := bson.M{"$or": bson.A{
		bson.M{"status": "pending", "next_attempt_at": bson.M{"$lte": now}},
		bson.M{"status": "sending", "lease_until": bson.M{"$lte": now}},
	}}
	update := bson.M{"$set": bson.M{"status": "sending", "lease_until": now.Add(time.Minute)}, "$inc": bson.M{"attempts": 1}}
	var row outboxRecord
	err := o.collection.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetReturnDocument(options.After)).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	m, err := o.decode(row.Payload)
	if err != nil {
		o.log.Error("mail outbox payload cannot be opened", "id", row.ID, "error", err)
		o.updateStatus(row.ID, "failed", time.Time{})
		return true, nil
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = o.transport.Send(sendCtx, m)
	cancel()
	if err == nil {
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = o.collection.DeleteOne(finishCtx, bson.M{"_id": row.ID, "status": "sending"})
		finishCancel()
		if err != nil {
			return true, fmt.Errorf("remove sent email: %w", err)
		}
		return true, nil
	}
	o.log.Error("mail delivery failed; will retry", "id", row.ID, "attempt", row.Attempts, "error", err)
	delay := time.Duration(1<<min(row.Attempts, 8)) * time.Second
	o.updateStatus(row.ID, "pending", time.Now().UTC().Add(delay))
	return true, nil
}

func (o *Outbox) updateStatus(id, status string, next time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	set := bson.M{"status": status}
	if status == "pending" {
		set["next_attempt_at"] = next
	}
	if _, err := o.collection.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set, "$unset": bson.M{"lease_until": ""}}); err != nil {
		o.log.Error("mail outbox update failed", "id", id, "error", err)
	}
}

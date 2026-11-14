// Package mongodb manages the MongoDB connection and collection indexes.
package mongodb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Database struct {
	Client *mongo.Client
	DB     *mongo.Database
}

// Open connects to MongoDB and verifies that the server is reachable.
func Open(ctx context.Context, uri, name string, maxPoolSize uint64) (*Database, error) {
	u, err := url.Parse(uri)
	if err != nil || (u.Scheme != "mongodb" && u.Scheme != "mongodb+srv") {
		return nil, errors.New("MONGODB_URI must be a mongodb:// or mongodb+srv:// URL")
	}
	if name == "" {
		return nil, errors.New("MONGODB_DATABASE is required")
	}
	opts := options.Client().ApplyURI(uri).SetServerSelectionTimeout(10 * time.Second)
	if maxPoolSize > 0 {
		opts.SetMaxPoolSize(maxPoolSize)
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("connect to MongoDB: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping MongoDB: %w", err)
	}
	return &Database{Client: client, DB: client.Database(name)}, nil
}

func (d *Database) Close(ctx context.Context) error { return d.Client.Disconnect(ctx) }

// InTx runs all operations in fn in one MongoDB transaction. The context
// passed to fn carries the session and must be used for every operation.
func (d *Database) InTx(ctx context.Context, fn func(context.Context) error) error {
	session, err := d.Client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return nil, fn(tx)
	})
	return err
}

// EnsureIndexes creates the indexes needed for ownership, common filters, and
// uniqueness. It is safe to call on every start.
func (d *Database) EnsureIndexes(ctx context.Context) error {
	indexes := map[string][]mongo.IndexModel{
		"users": {
			{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true).SetCollation(&options.Collation{Locale: "en", Strength: 2})},
		},
		"income_expectations":       {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "scope", Value: 1}, {Key: "month", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"user_sessions":             {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "revoked_at", Value: 1}}}},
		"refresh_tokens":            {{Keys: bson.D{{Key: "session_id", Value: 1}}}, {Keys: bson.D{{Key: "token_hash", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"email_verifications":       {{Keys: bson.D{{Key: "user_id", Value: 1}}}, {Keys: bson.D{{Key: "token_hash", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"mail_outbox":               {{Keys: bson.D{{Key: "status", Value: 1}, {Key: "next_attempt_at", Value: 1}, {Key: "created_at", Value: 1}}}},
		"password_resets":           {{Keys: bson.D{{Key: "user_id", Value: 1}}}, {Keys: bson.D{{Key: "token_hash", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"audit_events":              {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"categories":                {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"loans":                     {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"goals":                     {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"planned_payments":          {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"planned_payment_overrides": {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "planned_payment_id", Value: 1}, {Key: "month", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"planned_payment_skips":     {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "planned_payment_id", Value: 1}, {Key: "month", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"recurring_transactions":    {{Keys: bson.D{{Key: "active", Value: 1}, {Key: "start_date", Value: 1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"expenses":                  {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "occurred_on", Value: -1}, {Key: "created_at", Value: -1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "category_id", Value: 1}, {Key: "occurred_on", Value: 1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "loan_id", Value: 1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "goal_id", Value: 1}}}, {Keys: bson.D{{Key: "recurring_id", Value: 1}, {Key: "occurred_on", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"recurring_id": bson.M{"$type": "string"}})}},
		"income":                    {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "occurred_on", Value: -1}, {Key: "created_at", Value: -1}}}, {Keys: bson.D{{Key: "recurring_id", Value: 1}, {Key: "occurred_on", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"recurring_id": bson.M{"$type": "string"}})}},
		"budgets":                   {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"budget_categories":         {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "budget_id", Value: 1}, {Key: "category_id", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"savings_accounts":          {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"savings_transactions":      {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "account_id", Value: 1}, {Key: "occurred_on", Value: -1}}}},
		"subscriptions":             {{Keys: bson.D{{Key: "paystack_customer_code", Value: 1}}, Options: options.Index().SetSparse(true)}},
		"payments":                  {{Keys: bson.D{{Key: "reference", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"vaults":                    {{Keys: bson.D{{Key: "user_id", Value: 1}}, Options: options.Index().SetUnique(true)}},
	}
	for collection, models := range indexes {
		if _, err := d.DB.Collection(collection).Indexes().CreateMany(ctx, models); err != nil {
			return fmt.Errorf("create %s indexes: %w", collection, err)
		}
	}
	return nil
}

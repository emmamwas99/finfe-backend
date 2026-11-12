package mongodb

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"finfe/internal/domain/model"
)

type AuthStore struct{ DB *mongo.Database }

type userDoc struct {
	ID                  string     `bson:"_id"`
	Email               string     `bson:"email"`
	PasswordHash        string     `bson:"password_hash"`
	Name                string     `bson:"name"`
	EmailVerifiedAt     *time.Time `bson:"email_verified_at"`
	Currency            string     `bson:"currency"`
	Country             string     `bson:"country"`
	Timezone            string     `bson:"timezone"`
	Language            string     `bson:"language"`
	ExpectedIncomeMinor int64      `bson:"expected_income_minor"`
	WarnAtBp            int32      `bson:"warn_at_bp"`
	AutoLockMinutes     int32      `bson:"auto_lock_minutes"`
	LastBackupAt        *time.Time `bson:"last_backup_at"`
	CreatedAt           time.Time  `bson:"created_at"`
	UpdatedAt           time.Time  `bson:"updated_at"`
}

func (d userDoc) model() (model.User, error) {
	id, err := uuid.Parse(d.ID)
	if err != nil {
		return model.User{}, err
	}
	return model.User{ID: id, Email: d.Email, PasswordHash: d.PasswordHash, Name: d.Name, EmailVerifiedAt: d.EmailVerifiedAt, Currency: d.Currency, Country: d.Country, Timezone: d.Timezone, Language: d.Language, ExpectedIncomeMinor: d.ExpectedIncomeMinor, WarnAtBp: d.WarnAtBp, AutoLockMinutes: d.AutoLockMinutes, LastBackupAt: d.LastBackupAt, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}, nil
}

func (s AuthStore) CreateUser(ctx context.Context, arg model.CreateUserParams) (model.User, error) {
	now := time.Now().UTC()
	doc := userDoc{ID: arg.ID.String(), Email: strings.ToLower(strings.TrimSpace(arg.Email)), PasswordHash: arg.PasswordHash, Name: arg.Name, Currency: arg.Currency, Country: arg.Country, Timezone: arg.Timezone, Language: arg.Language, ExpectedIncomeMinor: arg.ExpectedIncomeMinor, WarnAtBp: 8000, AutoLockMinutes: 5, CreatedAt: now, UpdatedAt: now}
	_, err := s.DB.Collection("users").InsertOne(ctx, doc)
	if err != nil {
		return model.User{}, err
	}
	return doc.model()
}

func (s AuthStore) GetUserByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	var doc userDoc
	err := s.DB.Collection("users").FindOne(ctx, bson.M{"_id": id.String()}).Decode(&doc)
	if err != nil {
		return model.User{}, err
	}
	return doc.model()
}

func (s AuthStore) GetUserByEmail(ctx context.Context, email string) (model.User, error) {
	var doc userDoc
	err := s.DB.Collection("users").FindOne(ctx, bson.M{"email": strings.ToLower(strings.TrimSpace(email))}).Decode(&doc)
	if err != nil {
		return model.User{}, err
	}
	return doc.model()
}

func (s AuthStore) UpdateUser(ctx context.Context, arg model.UpdateUserParams) (model.User, error) {
	set := bson.M{"name": arg.Name, "currency": arg.Currency, "country": arg.Country, "timezone": arg.Timezone, "language": arg.Language, "expected_income_minor": arg.ExpectedIncomeMinor, "warn_at_bp": arg.WarnAtBp, "auto_lock_minutes": arg.AutoLockMinutes, "last_backup_at": arg.LastBackupAt, "updated_at": time.Now().UTC()}
	var doc userDoc
	err := s.DB.Collection("users").FindOneAndUpdate(ctx, bson.M{"_id": arg.ID.String()}, bson.M{"$set": set}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&doc)
	if err != nil {
		return model.User{}, err
	}
	return doc.model()
}

func (s AuthStore) MarkEmailVerified(ctx context.Context, id uuid.UUID) error {
	_, err := s.DB.Collection("users").UpdateOne(ctx, bson.M{"_id": id.String(), "email_verified_at": nil}, bson.M{"$set": bson.M{"email_verified_at": time.Now().UTC(), "updated_at": time.Now().UTC()}})
	return err
}

func (s AuthStore) SetUserPassword(ctx context.Context, arg model.SetUserPasswordParams) error {
	_, err := s.DB.Collection("users").UpdateOne(ctx, bson.M{"_id": arg.ID.String()}, bson.M{"$set": bson.M{"password_hash": arg.PasswordHash, "updated_at": time.Now().UTC()}})
	return err
}

func (s AuthStore) DeleteUser(ctx context.Context, id uuid.UUID) error {
	session, err := s.DB.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		cur, err := s.DB.Collection("user_sessions").Find(tx, bson.M{"user_id": id.String()}, options.Find().SetProjection(bson.M{"_id": 1}))
		if err != nil {
			return nil, err
		}
		var sessions []struct {
			ID string `bson:"_id"`
		}
		if err := cur.All(tx, &sessions); err != nil {
			return nil, err
		}
		ids := make(bson.A, 0, len(sessions))
		for _, item := range sessions {
			ids = append(ids, item.ID)
		}
		if len(ids) > 0 {
			if _, err := s.DB.Collection("refresh_tokens").DeleteMany(tx, bson.M{"session_id": bson.M{"$in": ids}}); err != nil {
				return nil, err
			}
		}
		for _, collection := range []string{"income_expectations", "user_sessions", "email_verifications", "password_resets", "audit_events", "categories", "loans", "goals", "planned_payments", "recurring_transactions", "expenses", "income", "budgets", "budget_categories", "savings_accounts", "savings_transactions", "subscriptions", "payments", "vaults"} {
			if _, err := s.DB.Collection(collection).DeleteMany(tx, bson.M{"user_id": id.String()}); err != nil {
				return nil, err
			}
		}
		if _, err := s.DB.Collection("users").DeleteOne(tx, bson.M{"_id": id.String()}); err != nil {
			return nil, err
		}
		return nil, nil
	})
	return err
}

func (s AuthStore) CreateSubscription(ctx context.Context, arg model.CreateSubscriptionParams) error {
	now := time.Now().UTC()
	_, err := s.DB.Collection("subscriptions").InsertOne(ctx, bson.M{"_id": arg.UserID.String(), "user_id": arg.UserID.String(), "status": "trialing", "trial_ends_at": arg.TrialEndsAt, "current_period_end": nil, "billing": nil, "period": nil, "paystack_customer_code": nil, "paystack_subscription_code": nil, "paystack_email_token": nil, "created_at": now, "updated_at": now})
	return err
}

type sessionDoc struct {
	ID         string     `bson:"_id"`
	UserID     string     `bson:"user_id"`
	UserAgent  string     `bson:"user_agent"`
	IP         string     `bson:"ip"`
	CreatedAt  time.Time  `bson:"created_at"`
	LastUsedAt time.Time  `bson:"last_used_at"`
	ExpiresAt  time.Time  `bson:"expires_at"`
	RevokedAt  *time.Time `bson:"revoked_at"`
}

func (d sessionDoc) model() (model.UserSession, error) {
	id, err := uuid.Parse(d.ID)
	if err != nil {
		return model.UserSession{}, err
	}
	userID, err := uuid.Parse(d.UserID)
	if err != nil {
		return model.UserSession{}, err
	}
	return model.UserSession{ID: id, UserID: userID, UserAgent: d.UserAgent, Ip: d.IP, CreatedAt: d.CreatedAt, LastUsedAt: d.LastUsedAt, ExpiresAt: d.ExpiresAt, RevokedAt: d.RevokedAt}, nil
}

func (s AuthStore) CreateSession(ctx context.Context, arg model.CreateSessionParams) (model.UserSession, error) {
	now := time.Now().UTC()
	doc := sessionDoc{ID: arg.ID.String(), UserID: arg.UserID.String(), UserAgent: arg.UserAgent, IP: arg.Ip, CreatedAt: now, LastUsedAt: now, ExpiresAt: arg.ExpiresAt}
	_, err := s.DB.Collection("user_sessions").InsertOne(ctx, doc)
	if err != nil {
		return model.UserSession{}, err
	}
	return doc.model()
}

func (s AuthStore) GetActiveSession(ctx context.Context, id uuid.UUID) (model.UserSession, error) {
	var doc sessionDoc
	err := s.DB.Collection("user_sessions").FindOne(ctx, bson.M{"_id": id.String(), "revoked_at": nil, "expires_at": bson.M{"$gt": time.Now().UTC()}}).Decode(&doc)
	if err != nil {
		return model.UserSession{}, err
	}
	return doc.model()
}

func (s AuthStore) ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]model.UserSession, error) {
	cur, err := s.DB.Collection("user_sessions").Find(ctx, bson.M{"user_id": userID.String(), "revoked_at": nil, "expires_at": bson.M{"$gt": time.Now().UTC()}}, options.Find().SetSort(bson.D{{Key: "last_used_at", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := make([]model.UserSession, 0)
	for cur.Next(ctx) {
		var doc sessionDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		model, err := doc.model()
		if err != nil {
			return nil, err
		}
		out = append(out, model)
	}
	return out, cur.Err()
}

func (s AuthStore) RevokeSession(ctx context.Context, arg model.RevokeSessionParams) (int64, error) {
	r, err := s.DB.Collection("user_sessions").UpdateOne(ctx, bson.M{"_id": arg.ID.String(), "user_id": arg.UserID.String(), "revoked_at": nil}, bson.M{"$set": bson.M{"revoked_at": time.Now().UTC()}})
	if err != nil {
		return 0, err
	}
	return r.ModifiedCount, nil
}

func (s AuthStore) RevokeOtherSessions(ctx context.Context, userID, exceptID uuid.UUID) error {
	_, err := s.DB.Collection("user_sessions").UpdateMany(ctx, bson.M{"user_id": userID.String(), "_id": bson.M{"$ne": exceptID.String()}, "revoked_at": nil}, bson.M{"$set": bson.M{"revoked_at": time.Now().UTC()}})
	return err
}

func (s AuthStore) TouchSession(ctx context.Context, arg model.TouchSessionParams) error {
	_, err := s.DB.Collection("user_sessions").UpdateOne(ctx, bson.M{"_id": arg.ID.String(), "revoked_at": nil}, bson.M{"$set": bson.M{"last_used_at": time.Now().UTC(), "expires_at": arg.ExpiresAt}})
	return err
}

func (s AuthStore) CreateRefreshToken(ctx context.Context, arg model.CreateRefreshTokenParams) error {
	_, err := s.DB.Collection("refresh_tokens").InsertOne(ctx, bson.M{"_id": arg.ID.String(), "session_id": arg.SessionID.String(), "token_hash": arg.TokenHash, "expires_at": arg.ExpiresAt, "used_at": nil, "created_at": time.Now().UTC()})
	return err
}

type refreshDoc struct {
	ID        string     `bson:"_id"`
	SessionID string     `bson:"session_id"`
	ExpiresAt time.Time  `bson:"expires_at"`
	UsedAt    *time.Time `bson:"used_at"`
}

func (s AuthStore) GetRefreshToken(ctx context.Context, hash []byte) (model.GetRefreshTokenRow, error) {
	var doc refreshDoc
	err := s.DB.Collection("refresh_tokens").FindOne(ctx, bson.M{"token_hash": hash}).Decode(&doc)
	if err != nil {
		return model.GetRefreshTokenRow{}, err
	}
	id, err := uuid.Parse(doc.ID)
	if err != nil {
		return model.GetRefreshTokenRow{}, err
	}
	sid, err := uuid.Parse(doc.SessionID)
	if err != nil {
		return model.GetRefreshTokenRow{}, err
	}
	session, err := s.GetSession(ctx, sid)
	if err != nil {
		return model.GetRefreshTokenRow{}, err
	}
	return model.GetRefreshTokenRow{ID: id, SessionID: sid, ExpiresAt: doc.ExpiresAt, UsedAt: doc.UsedAt, UserID: session.UserID, SessionRevokedAt: session.RevokedAt, SessionExpiresAt: session.ExpiresAt}, nil
}

func (s AuthStore) GetSession(ctx context.Context, id uuid.UUID) (model.UserSession, error) {
	var doc sessionDoc
	err := s.DB.Collection("user_sessions").FindOne(ctx, bson.M{"_id": id.String()}).Decode(&doc)
	if err != nil {
		return model.UserSession{}, err
	}
	return doc.model()
}

func (s AuthStore) UseRefreshToken(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	err := s.DB.Collection("refresh_tokens").FindOneAndUpdate(ctx, bson.M{"_id": id.String(), "used_at": nil}, bson.M{"$set": bson.M{"used_at": time.Now().UTC()}}).Err()
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (s AuthStore) CreateEmailVerification(ctx context.Context, arg model.CreateEmailVerificationParams) error {
	_, err := s.DB.Collection("email_verifications").InsertOne(ctx, bson.M{"_id": hex.EncodeToString(arg.TokenHash), "token_hash": arg.TokenHash, "user_id": arg.UserID.String(), "expires_at": arg.ExpiresAt, "used_at": nil, "created_at": time.Now().UTC()})
	return err
}

func (s AuthStore) CreatePasswordReset(ctx context.Context, arg model.CreatePasswordResetParams) error {
	_, err := s.DB.Collection("password_resets").InsertOne(ctx, bson.M{"_id": hex.EncodeToString(arg.TokenHash), "token_hash": arg.TokenHash, "user_id": arg.UserID.String(), "expires_at": arg.ExpiresAt, "used_at": nil, "created_at": time.Now().UTC()})
	return err
}

func (s AuthStore) useToken(ctx context.Context, collection string, hash []byte) (uuid.UUID, error) {
	var doc struct {
		UserID string `bson:"user_id"`
	}
	err := s.DB.Collection(collection).FindOneAndUpdate(ctx, bson.M{"_id": hex.EncodeToString(hash), "used_at": nil, "expires_at": bson.M{"$gt": time.Now().UTC()}}, bson.M{"$set": bson.M{"used_at": time.Now().UTC()}}).Decode(&doc)
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(doc.UserID)
}

func (s AuthStore) UseEmailVerification(ctx context.Context, hash []byte) (uuid.UUID, error) {
	return s.useToken(ctx, "email_verifications", hash)
}

func (s AuthStore) UsePasswordReset(ctx context.Context, hash []byte) (uuid.UUID, error) {
	return s.useToken(ctx, "password_resets", hash)
}

func (s AuthStore) InvalidatePasswordResets(ctx context.Context, userID uuid.UUID) error {
	_, err := s.DB.Collection("password_resets").UpdateMany(ctx, bson.M{"user_id": userID.String(), "used_at": nil}, bson.M{"$set": bson.M{"used_at": time.Now().UTC()}})
	return err
}

func (s AuthStore) InsertAuditEvent(ctx context.Context, arg model.InsertAuditEventParams) error {
	var uid any
	if arg.UserID != nil {
		uid = arg.UserID.String()
	}
	var metadata any
	if len(arg.Metadata) > 0 {
		if err := json.Unmarshal(arg.Metadata, &metadata); err != nil {
			return err
		}
	}
	_, err := s.DB.Collection("audit_events").InsertOne(ctx, bson.M{"user_id": uid, "action": arg.Action, "ip": arg.Ip, "user_agent": arg.UserAgent, "metadata": metadata, "created_at": time.Now().UTC()})
	return err
}

func (s AuthStore) ListIncomeExpectations(ctx context.Context, userID uuid.UUID) ([]model.IncomeExpectation, error) {
	cur, err := s.DB.Collection("income_expectations").Find(ctx, bson.M{"user_id": userID.String()}, options.Find().SetSort(bson.D{{Key: "month", Value: 1}, {Key: "scope", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := make([]model.IncomeExpectation, 0)
	for cur.Next(ctx) {
		var doc struct {
			Scope       string    `bson:"scope"`
			Month       time.Time `bson:"month"`
			AmountMinor int64     `bson:"amount_minor"`
		}
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		out = append(out, model.IncomeExpectation{UserID: userID, Scope: doc.Scope, Month: doc.Month, AmountMinor: doc.AmountMinor})
	}
	return out, cur.Err()
}

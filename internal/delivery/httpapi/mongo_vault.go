package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"finfe/internal/infrastructure/mongodb"
)

// mongoVaultStore keeps the encrypted backup and readable projection in one
// MongoDB transaction. Callers must use a replica set deployment.
type mongoVaultStore struct{ db *mongo.Database }

func (s mongoVaultStore) get(ctx context.Context, uid uuid.UUID) (vault, error) {
	var d struct {
		Version     int64     `bson:"version"`
		KDF         string    `bson:"kdf"`
		WrappedKey  string    `bson:"wrapped_key"`
		RecoveryKey string    `bson:"recovery_key"`
		Data        string    `bson:"data"`
		UpdatedAt   time.Time `bson:"updated_at"`
	}
	err := s.db.Collection("vaults").FindOne(ctx, bson.M{"_id": uid.String()}).Decode(&d)
	if err != nil {
		return vault{}, err
	}
	return vault{Version: d.Version, KDF: json.RawMessage(d.KDF), WrappedKey: json.RawMessage(d.WrappedKey), RecoveryKey: json.RawMessage(d.RecoveryKey), Data: json.RawMessage(d.Data), UpdatedAt: d.UpdatedAt}, nil
}

func (s mongoVaultStore) create(ctx context.Context, uid uuid.UUID, v vault, finance json.RawMessage) (vault, error) {
	session, err := s.db.Client().StartSession()
	if err != nil {
		return vault{}, err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		now := time.Now().UTC()
		_, err := s.db.Collection("vaults").InsertOne(tx, bson.M{"_id": uid.String(), "user_id": uid.String(), "version": int64(1), "kdf": string(v.KDF), "wrapped_key": string(v.WrappedKey), "recovery_key": string(v.RecoveryKey), "data": string(v.Data), "updated_at": now})
		if err != nil {
			return nil, err
		}
		if err := s.replaceSnapshot(tx, uid, finance); err != nil {
			return nil, err
		}
		v.Version = 1
		v.UpdatedAt = now
		return nil, nil
	})
	return v, err
}

func (s mongoVaultStore) save(ctx context.Context, uid uuid.UUID, expectedVersion int64, data, wrappedKey, recoveryKey, finance json.RawMessage) (int64, error) {
	session, err := s.db.Client().StartSession()
	if err != nil {
		return 0, err
	}
	defer session.EndSession(ctx)
	var next int64
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		set := bson.M{"updated_at": time.Now().UTC()}
		if len(data) > 0 {
			set["data"] = string(data)
		}
		if len(wrappedKey) > 0 {
			set["wrapped_key"] = string(wrappedKey)
		}
		if len(recoveryKey) > 0 {
			set["recovery_key"] = string(recoveryKey)
		}
		var out struct {
			Version int64 `bson:"version"`
		}
		err := s.db.Collection("vaults").FindOneAndUpdate(tx, bson.M{"_id": uid.String(), "version": expectedVersion}, bson.M{"$set": set, "$inc": bson.M{"version": int64(1)}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&out)
		if err != nil {
			return nil, err
		}
		next = out.Version
		if len(data) > 0 {
			if err := s.replaceSnapshot(tx, uid, finance); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return next, err
}

func (s mongoVaultStore) replaceSnapshot(ctx context.Context, uid uuid.UUID, raw json.RawMessage) error {
	var snap financeSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return fmt.Errorf("invalid finance snapshot: %w", err)
	}
	if len(snap.Categories) > 500 || len(snap.Expenses) > 100000 || len(snap.Incomes) > 100000 || len(snap.Loans) > 1000 || len(snap.Goals) > 1000 || len(snap.Bills) > 5000 {
		return errors.New("finance snapshot exceeds limits")
	}
	if err := validateSnapshotFields(snap); err != nil {
		return err
	}
	var user struct {
		Currency string `bson:"currency"`
	}
	if err := s.db.Collection("users").FindOne(ctx, bson.M{"_id": uid.String()}).Decode(&user); err != nil {
		return err
	}
	currency := strings.TrimSpace(user.Currency)
	owner := uid.String()
	if err := s.reconcileProjectionReferences(ctx, uid, snap); err != nil {
		return err
	}
	for _, collection := range []string{"expenses", "income", "planned_payment_overrides", "planned_payment_skips", "planned_payments", "budget_categories", "budgets", "goals", "loans", "categories"} {
		if _, err := s.db.Collection(collection).DeleteMany(ctx, bson.M{"user_id": owner, "sync_origin": "vault"}); err != nil {
			return err
		}
	}
	expected, err := minor(snap.Settings.ExpectedIncome)
	if err != nil || expected < 0 {
		return errors.New("invalid expected income")
	}
	warn, err := scaled(snap.Settings.WarnAt, 10000)
	if err != nil || warn < 1000 || warn > 10000 || snap.Settings.AutoLockMinutes < 1 || snap.Settings.AutoLockMinutes > 1440 {
		return errors.New("invalid profile settings")
	}
	if _, err := s.db.Collection("users").UpdateOne(ctx, bson.M{"_id": owner}, bson.M{"$set": bson.M{"name": snap.Settings.Name, "expected_income_minor": expected, "warn_at_bp": int32(warn), "auto_lock_minutes": snap.Settings.AutoLockMinutes, "updated_at": time.Now().UTC()}}); err != nil {
		return err
	}
	if _, err := s.db.Collection("income_expectations").DeleteMany(ctx, bson.M{"user_id": owner}); err != nil {
		return err
	}
	insert := func(collection, id string, fields bson.M) error {
		now := time.Now().UTC()
		doc := bson.M{"_id": id, "id": id, "user_id": owner, "sync_origin": "vault", "created_at": now, "updated_at": now}
		for k, v := range fields {
			doc[k] = v
		}
		_, err := s.db.Collection(collection).InsertOne(ctx, doc)
		return err
	}
	for _, item := range snap.Settings.IncomePlan {
		month, err := optionalMonth(item.From)
		if err != nil {
			return err
		}
		if month == nil {
			return errors.New("invalid income plan month")
		}
		amount, err := minor(item.Amount)
		if err != nil || amount < 0 {
			return errors.New("invalid income plan")
		}
		if err := insert("income_expectations", owner+":from:"+monthKey(month), bson.M{"scope": "from", "month": month, "amount_minor": amount}); err != nil {
			return err
		}
	}
	for key, value := range snap.Settings.IncomeOverrides {
		month, err := optionalMonth(key)
		if err != nil {
			return err
		}
		if month == nil {
			return errors.New("invalid income override month")
		}
		amount, err := minor(value)
		if err != nil || amount < 0 {
			return errors.New("invalid income override")
		}
		if err := insert("income_expectations", owner+":only:"+monthKey(month), bson.M{"scope": "only", "month": month, "amount_minor": amount}); err != nil {
			return err
		}
	}
	refs := mongodb.ResourceStore{DB: s.db}
	for _, c := range snap.Categories {
		id, err := requiredID(c.ID)
		if err != nil {
			return err
		}
		budget, err := minor(c.Budget)
		if err != nil || budget < 0 {
			return errors.New("invalid category budget")
		}
		if err := insert("categories", id.String(), bson.M{"name": c.Name, "emoji": c.Emoji, "kind": c.Kind, "archived": c.Archived}); err != nil {
			return err
		}
		if budget > 0 {
			bid := uuid.NewSHA1(uuid.NameSpaceOID, []byte("finfe-category-budget:"+c.ID)).String()
			if err := insert("budgets", bid, bson.M{"name": c.Name, "period": "monthly", "amount_minor": budget, "currency": currency, "alert_threshold_bp": int32(8000)}); err != nil {
				return err
			}
			if err := insert("budget_categories", bid+":"+id.String(), bson.M{"budget_id": bid, "category_id": id.String()}); err != nil {
				return err
			}
		}
	}
	for _, l := range snap.Loans {
		id, err := requiredID(l.ID)
		if err != nil {
			return err
		}
		amount, err := minor(l.Amount)
		if err != nil || amount <= 0 {
			return errors.New("invalid loan amount")
		}
		payment, err := minor(l.MonthlyPayment)
		if err != nil || payment < 0 {
			return errors.New("invalid loan payment")
		}
		rate, err := scaled(l.InterestRate, 100)
		if err != nil || rate < 0 || rate > 100000 {
			return errors.New("invalid interest rate")
		}
		if _, err := parseDate(l.StartDate); err != nil {
			return err
		}
		fields := bson.M{"name": l.Name, "lender": l.Lender, "principal_minor": amount, "currency": currency, "interest_rate_bp": int32(rate), "monthly_payment_minor": payment, "start_date": l.StartDate, "due_day": optionalDay(l.DueDay), "closed": l.Closed, "note": l.Note}
		if err := insert("loans", id.String(), fields); err != nil {
			return err
		}
	}
	for _, g := range snap.Goals {
		id, err := requiredID(g.ID)
		if err != nil {
			return err
		}
		target, err := minor(g.Target)
		if err != nil || target <= 0 {
			return errors.New("invalid goal target")
		}
		starting, err := minor(g.StartingAmount)
		if err != nil || starting < 0 {
			return errors.New("invalid starting amount")
		}
		month, err := optionalMonth(g.Deadline)
		if err != nil {
			return err
		}
		fields := bson.M{"name": g.Name, "emoji": g.Emoji, "target_minor": target, "starting_minor": starting, "currency": currency, "target_month": monthValue(month), "note": g.Note, "completed_at": nil}
		if err := insert("goals", id.String(), fields); err != nil {
			return err
		}
	}
	for _, b := range snap.Bills {
		id, err := requiredID(b.ID)
		if err != nil {
			return err
		}
		cat, err := requiredID(b.CategoryID)
		if err != nil {
			return err
		}
		amount, err := minor(b.Amount)
		if err != nil || amount <= 0 {
			return errors.New("invalid planned payment")
		}
		only, err := optionalMonth(b.OnlyMonth)
		if err != nil {
			return err
		}
		from, err := optionalMonth(b.FromMonth)
		if err != nil {
			return err
		}
		loan, err := optionalID(b.LoanID)
		if err != nil {
			return err
		}
		goal, err := optionalID(b.GoalID)
		if err != nil {
			return err
		}
		fields := bson.M{"name": b.Name, "amount_minor": amount, "currency": currency, "category_id": cat.String(), "due_day": b.DueDay, "months": b.Months, "only_month": monthValue(only), "from_month": monthValue(from), "loan_id": idValue(loan), "goal_id": idValue(goal), "active": b.Active}
		if err := refs.CheckReferences(ctx, "planned_payments", uid, fields); err != nil {
			return err
		}
		if err := insert("planned_payments", id.String(), fields); err != nil {
			return err
		}
		for key, value := range b.Overrides {
			month, err := optionalMonth(key)
			if err != nil {
				return err
			}
			if month == nil {
				return errors.New("invalid override month")
			}
			amt, err := minor(value)
			if err != nil || amt <= 0 {
				return errors.New("invalid override")
			}
			if err := insert("planned_payment_overrides", id.String()+":"+monthKey(month), bson.M{"planned_payment_id": id.String(), "month": month, "amount_minor": amt}); err != nil {
				return err
			}
		}
		for _, key := range b.Skips {
			month, err := optionalMonth(key)
			if err != nil {
				return err
			}
			if month == nil {
				return errors.New("invalid skip month")
			}
			if err := insert("planned_payment_skips", id.String()+":"+monthKey(month), bson.M{"planned_payment_id": id.String(), "month": month}); err != nil {
				return err
			}
		}
	}
	for _, e := range snap.Expenses {
		id, err := requiredID(e.ID)
		if err != nil {
			return err
		}
		if _, err := parseDate(e.Date); err != nil {
			return err
		}
		amount, err := minor(e.Amount)
		if err != nil || amount <= 0 {
			return errors.New("invalid expense")
		}
		cat, err := requiredID(e.CategoryID)
		if err != nil {
			return err
		}
		loan, err := optionalID(e.LoanID)
		if err != nil {
			return err
		}
		goal, err := optionalID(e.GoalID)
		if err != nil {
			return err
		}
		bill, err := optionalID(e.BillID)
		if err != nil {
			return err
		}
		fields := bson.M{"occurred_on": e.Date, "amount_minor": amount, "currency": currency, "category_id": cat.String(), "note": e.Note, "payment_method": nil, "loan_id": idValue(loan), "goal_id": idValue(goal), "planned_payment_id": idValue(bill)}
		if err := refs.CheckReferences(ctx, "expenses", uid, fields); err != nil {
			return err
		}
		if err := insert("expenses", id.String(), fields); err != nil {
			return err
		}
	}
	for _, i := range snap.Incomes {
		id, err := requiredID(i.ID)
		if err != nil {
			return err
		}
		if _, err := parseDate(i.Date); err != nil {
			return err
		}
		amount, err := minor(i.Amount)
		if err != nil || amount <= 0 {
			return errors.New("invalid income")
		}
		loan, err := optionalID(i.LoanID)
		if err != nil {
			return err
		}
		fields := bson.M{"occurred_on": i.Date, "amount_minor": amount, "currency": currency, "source": i.Source, "note": i.Note, "is_borrowed": i.IsLoan, "category_id": nil, "loan_id": idValue(loan)}
		if err := refs.CheckReferences(ctx, "income", uid, fields); err != nil {
			return err
		}
		if err := insert("income", id.String(), fields); err != nil {
			return err
		}
	}
	return nil
}

func validateSnapshotFields(s financeSnapshot) error {
	if len(s.Settings.Name) > 100 {
		return errors.New("invalid profile name")
	}
	validKind := map[string]bool{"need": true, "want": true, "debt": true, "invest": true, "saving": true, "income": true}
	for _, c := range s.Categories {
		if len(c.Name) < 1 || len(c.Name) > 60 || len(c.Emoji) > 16 || !validKind[c.Kind] {
			return errors.New("invalid category")
		}
	}
	for _, l := range s.Loans {
		if len(l.Name) < 1 || len(l.Name) > 100 || len(l.Lender) > 100 || len(l.Note) > 1000 || l.DueDay < 0 || l.DueDay > 31 {
			return errors.New("invalid loan")
		}
	}
	for _, g := range s.Goals {
		if len(g.Name) < 1 || len(g.Name) > 100 || len(g.Emoji) > 16 || len(g.Note) > 1000 {
			return errors.New("invalid goal")
		}
	}
	for _, b := range s.Bills {
		if len(b.Name) < 1 || len(b.Name) > 100 || b.DueDay < 0 || b.DueDay > 31 {
			return errors.New("invalid planned payment")
		}
		for _, month := range b.Months {
			if month < 1 || month > 12 { return errors.New("invalid planned payment month") }
		}
	}
	for _, e := range s.Expenses {
		if len(e.Note) > 500 { return errors.New("invalid expense note") }
	}
	for _, i := range s.Incomes {
		if len(i.Source) < 1 || len(i.Source) > 60 || len(i.Note) > 500 { return errors.New("invalid income") }
	}
	return nil
}

// Keep direct API documents valid when a vault sync removes a referenced
// record. Required category references block the sync; optional references
// are cleared, matching the former relational deletion rules.
func (s mongoVaultStore) reconcileProjectionReferences(ctx context.Context, uid uuid.UUID, snap financeSnapshot) error {
	owner := uid.String()
	newIDs := map[string]map[string]bool{"categories": {}, "loans": {}, "goals": {}, "planned_payments": {}, "budgets": {}}
	for _, c := range snap.Categories {
		id, err := requiredID(c.ID)
		if err != nil {
			return err
		}
		newIDs["categories"][id.String()] = true
		budget, err := minor(c.Budget)
		if err != nil || budget < 0 {
			return errors.New("invalid category budget")
		}
		if budget > 0 {
			bid := uuid.NewSHA1(uuid.NameSpaceOID, []byte("finfe-category-budget:"+c.ID))
			newIDs["budgets"][bid.String()] = true
		}
	}
	for _, l := range snap.Loans {
		id, err := requiredID(l.ID)
		if err != nil {
			return err
		}
		newIDs["loans"][id.String()] = true
	}
	for _, g := range snap.Goals {
		id, err := requiredID(g.ID)
		if err != nil {
			return err
		}
		newIDs["goals"][id.String()] = true
	}
	for _, p := range snap.Bills {
		id, err := requiredID(p.ID)
		if err != nil {
			return err
		}
		newIDs["planned_payments"][id.String()] = true
	}
	for collection, ids := range newIDs {
		cur, err := s.db.Collection(collection).Find(ctx, bson.M{"user_id": owner, "sync_origin": "vault"}, options.Find().SetProjection(bson.M{"_id": 1}))
		if err != nil {
			return err
		}
		var old []struct {
			ID string `bson:"_id"`
		}
		if err := cur.All(ctx, &old); err != nil {
			return err
		}
		for _, item := range old {
			if ids[item.ID] {
				continue
			}
			switch collection {
			case "categories":
				for _, dependent := range []string{"expenses", "income", "planned_payments", "recurring_transactions"} {
					n, err := s.db.Collection(dependent).CountDocuments(ctx, bson.M{"user_id": owner, "sync_origin": "api", "category_id": item.ID})
					if err != nil {
						return err
					}
					if n > 0 {
						return mongodb.ErrInUse
					}
				}
				if _, err := s.db.Collection("budget_categories").DeleteMany(ctx, bson.M{"user_id": owner, "category_id": item.ID}); err != nil {
					return err
				}
			case "loans", "goals", "planned_payments":
				field := map[string]string{"loans": "loan_id", "goals": "goal_id", "planned_payments": "planned_payment_id"}[collection]
				for _, dependent := range []string{"expenses", "income", "planned_payments"} {
					if dependent == "income" && field != "loan_id" || dependent == "planned_payments" && field == "planned_payment_id" {
						continue
					}
					if _, err := s.db.Collection(dependent).UpdateMany(ctx, bson.M{"user_id": owner, "sync_origin": "api", field: item.ID}, bson.M{"$set": bson.M{field: nil}}); err != nil {
						return err
					}
				}
			case "budgets":
				if _, err := s.db.Collection("budget_categories").DeleteMany(ctx, bson.M{"user_id": owner, "budget_id": item.ID}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func idValue(v any) any {
	if id, ok := v.(uuid.UUID); ok {
		return id.String()
	}
	return nil
}
func monthValue(v any) any {
	if t, ok := v.(time.Time); ok {
		return t.Format("2006-01-02")
	}
	return nil
}
func monthKey(v any) string { return v.(time.Time).Format("2006-01-02") }

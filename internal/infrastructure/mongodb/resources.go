package mongodb

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var ErrInUse = errors.New("record is still in use")
var ErrInvalidReference = errors.New("reference does not belong to this account")
var ErrValidation = errors.New("invalid resource values")

// References names the collections checked for ownership before a finance
// document is written. MongoDB has no foreign keys, so writes enforce these.
var References = map[string]map[string]string{
	"expenses":               {"category_id": "categories", "loan_id": "loans", "goal_id": "goals", "planned_payment_id": "planned_payments", "recurring_id": "recurring_transactions"},
	"income":                 {"category_id": "categories", "loan_id": "loans", "recurring_id": "recurring_transactions"},
	"planned_payments":       {"category_id": "categories", "loan_id": "loans", "goal_id": "goals"},
	"recurring_transactions": {"category_id": "categories"},
}

var resourceDefaults = map[string]bson.M{
	"categories":             {"emoji": "•", "archived": false},
	"expenses":               {"note": "", "payment_method": nil, "loan_id": nil, "goal_id": nil, "planned_payment_id": nil, "recurring_id": nil},
	"income":                 {"note": "", "is_borrowed": false, "category_id": nil, "loan_id": nil, "recurring_id": nil},
	"budgets":                {"alert_threshold_bp": int64(8000)},
	"savings_accounts":       {"is_emergency_fund": false, "target_minor": nil, "archived": false},
	"goals":                  {"emoji": "🎯", "starting_minor": int64(0), "target_month": nil, "note": "", "completed_at": nil},
	"loans":                  {"lender": "", "interest_rate_bp": int64(0), "monthly_payment_minor": int64(0), "due_day": nil, "closed": false, "note": ""},
	"planned_payments":       {"due_day": int64(1), "months": []int{}, "only_month": nil, "from_month": nil, "loan_id": nil, "goal_id": nil, "active": true},
	"recurring_transactions": {"category_id": nil, "source": "", "day_of_month": nil, "weekday": nil, "end_date": nil, "last_posted_on": nil, "active": true},
}

// ResourceStore provides the common finance CRUD operations. IDs are UUID
// strings in both _id and id; each operation includes user_id in its filter.
type ResourceStore struct{ DB *mongo.Database }

func (s ResourceStore) collection(name string) (*mongo.Collection, error) {
	switch name {
	case "categories", "expenses", "income", "budgets", "savings_accounts", "goals", "loans", "planned_payments", "recurring_transactions":
		return s.DB.Collection(name), nil
	default:
		return nil, fmt.Errorf("unknown resource %q", name)
	}
}

func (s ResourceStore) CheckReferences(ctx context.Context, collection string, userID uuid.UUID, fields bson.M) error {
	for field, target := range References[collection] {
		v, ok := fields[field]
		if !ok || v == nil || v == "" {
			continue
		}
		id, ok := v.(string)
		if !ok {
			return ErrInvalidReference
		}
		n, err := s.DB.Collection(target).CountDocuments(ctx, bson.M{"_id": id, "user_id": userID.String()})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrInvalidReference
		}
	}
	return nil
}

func validateResource(name string, doc bson.M) error {
	if name == "recurring_transactions" {
		kind, _ := doc["kind"].(string)
		frequency, _ := doc["frequency"].(string)
		category, _ := doc["category_id"].(string)
		if kind == "expense" && category == "" {
			return fmt.Errorf("%w: expense recurring rules require a category", ErrValidation)
		}
		if frequency == "monthly" {
			if n, ok := resourceInt(doc["day_of_month"]); !ok || n < 1 || n > 31 {
				return fmt.Errorf("%w: monthly recurring rules require day_of_month", ErrValidation)
			}
		} else if frequency == "weekly" {
			if n, ok := resourceInt(doc["weekday"]); !ok || n < 0 || n > 6 {
				return fmt.Errorf("%w: weekly recurring rules require weekday", ErrValidation)
			}
		}
		start, _ := doc["start_date"].(string)
		end, _ := doc["end_date"].(string)
		if end != "" && end < start {
			return fmt.Errorf("%w: end_date cannot precede start_date", ErrValidation)
		}
	}
	if name == "loans" {
		if n, ok := resourceInt(doc["interest_rate_bp"]); ok && n > 100000 {
			return fmt.Errorf("%w: interest_rate_bp is out of range", ErrValidation)
		}
		if doc["due_day"] != nil {
			if n, ok := resourceInt(doc["due_day"]); !ok || n < 1 || n > 31 {
				return fmt.Errorf("%w: due_day is out of range", ErrValidation)
			}
		}
	}
	return nil
}

func resourceInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int32:
		return int64(x), true
	case int:
		return int64(x), true
	default:
		return 0, false
	}
}

func (s ResourceStore) Create(ctx context.Context, name string, userID uuid.UUID, fields bson.M) (bson.M, error) {
	coll, err := s.collection(name)
	if err != nil {
		return nil, err
	}
	if err := s.CheckReferences(ctx, name, userID, fields); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	doc := bson.M{"_id": id, "id": id, "user_id": userID.String(), "created_at": now, "updated_at": now, "sync_origin": "api"}
	for k, v := range resourceDefaults[name] {
		doc[k] = v
	}
	for k, v := range fields {
		doc[k] = v
	}
	if err := validateResource(name, doc); err != nil {
		return nil, err
	}
	if _, err := coll.InsertOne(ctx, doc); err != nil {
		return nil, err
	}
	delete(doc, "_id")
	return doc, nil
}

func (s ResourceStore) Get(ctx context.Context, name string, userID, id uuid.UUID) (bson.M, error) {
	coll, err := s.collection(name)
	if err != nil {
		return nil, err
	}
	var doc bson.M
	err = coll.FindOne(ctx, bson.M{"_id": id.String(), "user_id": userID.String()}).Decode(&doc)
	if err != nil {
		return nil, err
	}
	delete(doc, "_id")
	return doc, nil
}

func (s ResourceStore) Update(ctx context.Context, name string, userID, id uuid.UUID, fields bson.M) (bson.M, error) {
	coll, err := s.collection(name)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, errors.New("at least one field is required")
	}
	if err := s.CheckReferences(ctx, name, userID, fields); err != nil {
		return nil, err
	}
	if name == "recurring_transactions" || name == "loans" {
		var current bson.M
		if err := coll.FindOne(ctx, bson.M{"_id": id.String(), "user_id": userID.String()}).Decode(&current); err != nil {
			return nil, err
		}
		for k, v := range fields {
			current[k] = v
		}
		if err := validateResource(name, current); err != nil {
			return nil, err
		}
	}
	set := bson.M{"updated_at": time.Now().UTC()}
	for k, v := range fields {
		set[k] = v
	}
	var doc bson.M
	err = coll.FindOneAndUpdate(ctx, bson.M{"_id": id.String(), "user_id": userID.String()}, bson.M{"$set": set}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&doc)
	if err != nil {
		return nil, err
	}
	delete(doc, "_id")
	return doc, nil
}

func (s ResourceStore) Delete(ctx context.Context, name string, userID, id uuid.UUID) error {
	coll, err := s.collection(name)
	if err != nil {
		return err
	}
	session, err := s.DB.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		owner := userID.String()
		key := id.String()
		// Category references restrict deletion, except budget associations,
		// which the relational schema removed with the category.
		if name == "categories" {
			for _, dep := range []string{"expenses", "income", "planned_payments", "recurring_transactions"} {
				n, err := s.DB.Collection(dep).CountDocuments(tx, bson.M{"user_id": owner, "category_id": key})
				if err != nil {
					return nil, err
				}
				if n > 0 {
					return nil, ErrInUse
				}
			}
		}
		// Optional references are cleared when their target is removed.
		for dependent, refs := range References {
			for field, target := range refs {
				if target != name || name == "categories" {
					continue
				}
				if _, err := s.DB.Collection(dependent).UpdateMany(tx, bson.M{"user_id": owner, field: key}, bson.M{"$set": bson.M{field: nil}}); err != nil {
					return nil, err
				}
			}
		}
		for _, cascade := range map[string][]string{"categories": {"budget_categories"}, "budgets": {"budget_categories"}, "savings_accounts": {"savings_transactions"}, "planned_payments": {"planned_payment_overrides", "planned_payment_skips"}}[name] {
			field := map[string]string{"categories": "category_id", "budgets": "budget_id", "savings_accounts": "account_id", "planned_payments": "planned_payment_id"}[name]
			if _, err := s.DB.Collection(cascade).DeleteMany(tx, bson.M{"user_id": owner, field: key}); err != nil {
				return nil, err
			}
		}
		result, err := coll.DeleteOne(tx, bson.M{"_id": key, "user_id": owner})
		if err != nil {
			return nil, err
		}
		if result.DeletedCount == 0 {
			return nil, mongo.ErrNoDocuments
		}
		return nil, nil
	})
	return err
}

type ResourceFilter struct {
	Equal map[string]string
	From  string
	To    string
	Query string
}

func (s ResourceStore) List(ctx context.Context, name string, userID uuid.UUID, f ResourceFilter, page, limit int) ([]bson.M, int64, error) {
	coll, err := s.collection(name)
	if err != nil {
		return nil, 0, err
	}
	if page < 1 || page > 100000 || limit < 1 || limit > 100 {
		return nil, 0, errors.New("invalid pagination")
	}
	filter := bson.M{"user_id": userID.String()}
	for k, v := range f.Equal {
		filter[k] = v
	}
	dateRange := bson.M{}
	if f.From != "" {
		dateRange["$gte"] = f.From
	}
	if f.To != "" {
		dateRange["$lte"] = f.To
	}
	if len(dateRange) > 0 {
		filter["occurred_on"] = dateRange
	}
	if name == "expenses" && f.Query != "" {
		filter["note"] = bson.M{"$regex": regexp.QuoteMeta(f.Query), "$options": "i"}
	}
	total, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	sort := bson.D{{Key: "created_at", Value: -1}}
	if name == "expenses" || name == "income" {
		sort = bson.D{{Key: "occurred_on", Value: -1}, {Key: "created_at", Value: -1}}
	}
	cur, err := coll.Find(ctx, filter, options.Find().SetSort(sort).SetSkip(int64((page-1)*limit)).SetLimit(int64(limit)))
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)
	data := make([]bson.M, 0)
	for cur.Next(ctx) {
		var doc bson.M
		if err := cur.Decode(&doc); err != nil {
			return nil, 0, err
		}
		delete(doc, "_id")
		data = append(data, doc)
	}
	return data, total, cur.Err()
}

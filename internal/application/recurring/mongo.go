package recurring

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"finfe/internal/domain/calendar"
	"finfe/internal/infrastructure/mongodb"
)

// RunMongo posts due recurring rules, with a deterministic occurrence ID and
// a unique index on (recurring_id, occurred_on) as idempotency guards.
func RunMongo(ctx context.Context, db *mongodb.Database, now time.Time) error {
	cur, err := db.DB.Collection("recurring_transactions").Find(ctx, bson.M{"active": true})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var doc bson.M
		if err := cur.Decode(&doc); err != nil {
			return err
		}
		r, err := mongoRule(ctx, db.DB, doc)
		if err != nil {
			return err
		}
		today := calendar.Today(now, r.Timezone)
		first := r.Start
		if r.Last != nil && !r.Last.Before(first) {
			first = r.Last.AddDate(0, 0, 1)
		}
		if cutoff := today.AddDate(0, 0, -366); first.Before(cutoff) {
			first = cutoff
		}
		for day := calendar.DateOnly(first); !day.After(today); day = day.AddDate(0, 0, 1) {
			if !due(r, day) {
				continue
			}
			if err := postMongo(ctx, db, r, day); err != nil {
				return fmt.Errorf("recurring %s on %s: %w", r.ID, day.Format(time.DateOnly), err)
			}
		}
	}
	return cur.Err()
}

func mongoRule(ctx context.Context, db *mongo.Database, doc bson.M) (rule, error) {
	str := func(key string) string { s, _ := doc[key].(string); return s }
	num := func(key string) int64 {
		switch x := doc[key].(type) {
		case int64:
			return x
		case int32:
			return int64(x)
		case int:
			return int64(x)
		default:
			return 0
		}
	}
	id, err := uuid.Parse(str("id"))
	if err != nil {
		return rule{}, err
	}
	uid, err := uuid.Parse(str("user_id"))
	if err != nil {
		return rule{}, err
	}
	start, err := time.Parse(time.DateOnly, str("start_date"))
	if err != nil {
		return rule{}, err
	}
	r := rule{ID: id, UserID: uid, Kind: str("kind"), Name: str("name"), Currency: str("currency"), Source: str("source"), Frequency: str("frequency"), Amount: num("amount_minor"), Start: start}
	if s := str("category_id"); s != "" {
		cat, err := uuid.Parse(s)
		if err != nil {
			return rule{}, err
		}
		r.CategoryID = &cat
	}
	if n := num("day_of_month"); n > 0 {
		x := int16(n)
		r.DayOfMonth = &x
	}
	if v := doc["weekday"]; v != nil {
		x := int16(num("weekday"))
		r.Weekday = &x
	}
	if s := str("end_date"); s != "" {
		t, err := time.Parse(time.DateOnly, s)
		if err != nil {
			return rule{}, err
		}
		r.End = &t
	}
	if s := str("last_posted_on"); s != "" {
		t, err := time.Parse(time.DateOnly, s)
		if err != nil {
			return rule{}, err
		}
		r.Last = &t
	}
	var user struct {
		Timezone string `bson:"timezone"`
	}
	if err := db.Collection("users").FindOne(ctx, bson.M{"_id": uid.String()}).Decode(&user); err != nil {
		return rule{}, err
	}
	r.Timezone = user.Timezone
	return r, nil
}

func postMongo(ctx context.Context, db *mongodb.Database, r rule, day time.Time) error {
	return db.InTx(ctx, func(tx context.Context) error {
		var doc bson.M
		if err := db.DB.Collection("recurring_transactions").FindOne(tx, bson.M{"_id": r.ID.String(), "active": true}).Decode(&doc); err != nil {
			if err == mongo.ErrNoDocuments {
				return nil
			}
			return err
		}
		date := day.Format(time.DateOnly)
		occurrence := uuid.NewSHA1(uuid.NameSpaceOID, []byte("finfe-recurring:"+r.ID.String()+":"+date)).String()
		now := time.Now().UTC()
		owner := r.UserID.String()
		category := any(nil)
		if r.CategoryID != nil {
			category = r.CategoryID.String()
		}
		fields := bson.M{"id": occurrence, "user_id": owner, "occurred_on": date, "amount_minor": r.Amount, "currency": r.Currency, "category_id": category, "recurring_id": r.ID.String(), "sync_origin": "api", "created_at": now, "updated_at": now}
		collection := "expenses"
		if r.Kind == "expense" {
			fields["note"] = r.Name
			fields["payment_method"] = nil
			fields["loan_id"] = nil
			fields["goal_id"] = nil
			fields["planned_payment_id"] = nil
		} else {
			collection = "income"
			source := r.Source
			if source == "" {
				source = r.Name
			}
			fields["source"] = source
			fields["note"] = ""
			fields["is_borrowed"] = false
			fields["loan_id"] = nil
		}
		if _, err := db.DB.Collection(collection).UpdateOne(tx, bson.M{"_id": occurrence}, bson.M{"$setOnInsert": fields}, options.UpdateOne().SetUpsert(true)); err != nil {
			return err
		}
		last := date
		if prior, ok := doc["last_posted_on"].(string); ok && prior > date {
			last = prior
		}
		_, err := db.DB.Collection("recurring_transactions").UpdateOne(tx, bson.M{"_id": r.ID.String(), "active": true}, bson.M{"$set": bson.M{"last_posted_on": last, "updated_at": now}})
		return err
	})
}

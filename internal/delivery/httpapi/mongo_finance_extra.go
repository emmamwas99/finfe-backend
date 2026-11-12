package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"finfe/internal/domain/calendar"
	"finfe/internal/infrastructure/mongodb"
)

func (a *MongoAPI) savingsTotals(ctx context.Context, owner, accountID string) (int64, int64, error) {
	transactions, err := a.docs(ctx, "savings_transactions", bson.M{"user_id": owner, "account_id": accountID})
	if err != nil {
		return 0, 0, err
	}
	var deposits, withdrawals int64
	for _, tx := range transactions {
		if stringField(tx, "kind") == "deposit" {
			deposits += int64Field(tx, "amount_minor")
		} else {
			withdrawals += int64Field(tx, "amount_minor")
		}
	}
	return deposits, withdrawals, nil
}

func (a *MongoAPI) listSavings(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	if page > 100000 {
		fail(w, 400, "invalid_page", "Page is too large")
		return
	}
	owner := principal(r).UserID
	data, total, err := a.resources.List(r.Context(), "savings_accounts", owner, mongodb.ResourceFilter{}, page, limit)
	if err != nil {
		a.serverError(w, err)
		return
	}
	for _, account := range data {
		deposits, withdrawals, err := a.savingsTotals(r.Context(), owner.String(), stringField(account, "id"))
		if err != nil {
			a.serverError(w, err)
			return
		}
		account["deposits_minor"] = deposits
		account["withdrawals_minor"] = withdrawals
		account["balance_minor"] = deposits - withdrawals
	}
	respond(w, 200, map[string]any{"data": data, "page": page, "limit": limit, "total": total})
}

func (a *MongoAPI) savingsProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	account, err := a.resources.Get(r.Context(), "savings_accounts", principal(r).UserID, id)
	if err != nil {
		a.resourceError(w, err)
		return
	}
	deposits, withdrawals, err := a.savingsTotals(r.Context(), principal(r).UserID.String(), id.String())
	if err != nil {
		a.serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"id": id, "currency": stringField(account, "currency"), "target_minor": account["target_minor"], "balance_minor": deposits - withdrawals})
}

func (a *MongoAPI) savingsTransactions(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	if _, err := a.resources.Get(r.Context(), "savings_accounts", principal(r).UserID, id); err != nil {
		a.resourceError(w, err)
		return
	}
	data, err := a.docs(r.Context(), "savings_transactions", bson.M{"user_id": principal(r).UserID.String(), "account_id": id.String()})
	if err != nil {
		a.serverError(w, err)
		return
	}
	sort.Slice(data, func(i, j int) bool {
		di, dj := stringField(data[i], "occurred_on"), stringField(data[j], "occurred_on")
		if di != dj {
			return di > dj
		}
		ti, _ := data[i]["created_at"].(time.Time)
		tj, _ := data[j]["created_at"].(time.Time)
		return ti.After(tj)
	})
	if len(data) > 100 {
		data = data[:100]
	}
	respond(w, 200, map[string]any{"data": data})
}

var errArchivedSavings = errors.New("savings account is archived")
var errInsufficientSavings = errors.New("withdrawal exceeds balance")

func (a *MongoAPI) addSavingsTransaction(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	var v struct {
		Kind        string `json:"kind"`
		AmountMinor int64  `json:"amount_minor"`
		OccurredOn  string `json:"occurred_on"`
		Note        string `json:"note"`
	}
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	if v.AmountMinor <= 0 || (v.Kind != "deposit" && v.Kind != "withdrawal") || len(v.Note) > 500 {
		fail(w, 400, "validation_failed", "Invalid savings transaction")
		return
	}
	if _, err := parseDate(v.OccurredOn); err != nil {
		badJSON(w, err)
		return
	}
	owner := principal(r).UserID.String()
	var out bson.M
	err := a.database.InTx(r.Context(), func(tx context.Context) error {
		account, err := a.resources.Get(tx, "savings_accounts", principal(r).UserID, id)
		if err != nil {
			return err
		}
		if boolField(account, "archived") {
			return errArchivedSavings
		}
		// Force concurrent withdrawals to contend on the account document.
		_, err = a.database.DB.Collection("savings_accounts").UpdateOne(tx, bson.M{"_id": id.String(), "user_id": owner}, bson.M{"$inc": bson.M{"transaction_version": int64(1)}})
		if err != nil {
			return err
		}
		if v.Kind == "withdrawal" {
			deposits, withdrawals, err := a.savingsTotals(tx, owner, id.String())
			if err != nil {
				return err
			}
			if v.AmountMinor > deposits-withdrawals {
				return errInsufficientSavings
			}
		}
		now := time.Now().UTC()
		txID := uuid.NewString()
		out = bson.M{"_id": txID, "id": txID, "user_id": owner, "account_id": id.String(), "kind": v.Kind, "amount_minor": v.AmountMinor, "occurred_on": v.OccurredOn, "note": v.Note, "created_at": now}
		_, err = a.database.DB.Collection("savings_transactions").InsertOne(tx, out)
		return err
	})
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		fail(w, 404, "not_found", "Savings account not found")
		return
	case errors.Is(err, errArchivedSavings):
		fail(w, 409, "archived", "Savings account is archived")
		return
	case errors.Is(err, errInsufficientSavings):
		fail(w, 409, "insufficient_funds", "Withdrawal exceeds balance")
		return
	case err != nil:
		a.serverError(w, err)
		return
	}
	delete(out, "_id")
	respond(w, 201, map[string]any{"data": out})
}

func (a *MongoAPI) goalProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	goal, err := a.resources.Get(r.Context(), "goals", principal(r).UserID, id)
	if err != nil {
		a.resourceError(w, err)
		return
	}
	expenses, err := a.docs(r.Context(), "expenses", bson.M{"user_id": principal(r).UserID.String(), "goal_id": id.String(), "currency": stringField(goal, "currency")})
	if err != nil {
		a.serverError(w, err)
		return
	}
	contributed := int64(0)
	for _, e := range expenses {
		contributed += int64Field(e, "amount_minor")
	}
	target, starting := int64Field(goal, "target_minor"), int64Field(goal, "starting_minor")
	saved := starting + contributed
	respond(w, 200, map[string]any{"goal_id": id, "currency": stringField(goal, "currency"), "target_minor": target, "saved_minor": saved, "remaining_minor": max(0, target-saved), "completed": saved >= target})
}

func (a *MongoAPI) goalContribution(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	var v struct {
		AmountMinor int64     `json:"amount_minor"`
		CategoryID  uuid.UUID `json:"category_id"`
		OccurredOn  string    `json:"occurred_on"`
		Note        string    `json:"note"`
	}
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	if _, err := parseDate(v.OccurredOn); err != nil || v.AmountMinor <= 0 || v.CategoryID == uuid.Nil {
		fail(w, 400, "validation_failed", "Invalid contribution")
		return
	}
	goal, err := a.resources.Get(r.Context(), "goals", principal(r).UserID, id)
	if err != nil {
		a.resourceError(w, err)
		return
	}
	row, err := a.resources.Create(r.Context(), "expenses", principal(r).UserID, bson.M{"occurred_on": v.OccurredOn, "amount_minor": v.AmountMinor, "currency": stringField(goal, "currency"), "category_id": v.CategoryID.String(), "note": v.Note, "goal_id": id.String()})
	if err != nil {
		a.resourceError(w, err)
		return
	}
	respond(w, 201, map[string]any{"data": row})
}

func (a *MongoAPI) budgetProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	m, err := a.monthRange(r)
	if err != nil {
		a.monthError(w, r, err)
		return
	}
	budget, err := a.resources.Get(r.Context(), "budgets", principal(r).UserID, id)
	if err != nil {
		a.resourceError(w, err)
		return
	}
	from, to := m.Range()
	if stringField(budget, "period") == "weekly" {
		u, err := a.authStore.GetUserByID(r.Context(), principal(r).UserID)
		if err != nil {
			a.serverError(w, err)
			return
		}
		today := calendar.Today(time.Now(), u.Timezone)
		from = today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
		to = from.AddDate(0, 0, 7)
	}
	links, err := a.docs(r.Context(), "budget_categories", bson.M{"user_id": principal(r).UserID.String(), "budget_id": id.String()})
	if err != nil {
		a.serverError(w, err)
		return
	}
	set := map[string]bool{}
	for _, link := range links {
		set[stringField(link, "category_id")] = true
	}
	expenses, err := a.docs(r.Context(), "expenses", bson.M{"user_id": principal(r).UserID.String(), "currency": stringField(budget, "currency"), "occurred_on": bson.M{"$gte": from.Format("2006-01-02"), "$lt": to.Format("2006-01-02")}})
	if err != nil {
		a.serverError(w, err)
		return
	}
	spent := int64(0)
	for _, e := range expenses {
		if set[stringField(e, "category_id")] {
			spent += int64Field(e, "amount_minor")
		}
	}
	amount := int64Field(budget, "amount_minor")
	respond(w, 200, map[string]any{"budget_id": id, "currency": stringField(budget, "currency"), "amount_minor": amount, "spent_minor": spent, "remaining_minor": amount - spent, "from": from.Format("2006-01-02"), "to": to.Format("2006-01-02")})
}

func (a *MongoAPI) setBudgetCategories(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(w, r)
	if !ok {
		return
	}
	var v struct {
		CategoryIDs []string `json:"category_ids"`
	}
	if err := decode(r, &v); err != nil {
		badJSON(w, err)
		return
	}
	if len(v.CategoryIDs) > 100 {
		fail(w, 400, "validation_failed", "Too many categories")
		return
	}
	seen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(v.CategoryIDs))
	for _, raw := range v.CategoryIDs {
		x, err := uuid.Parse(raw)
		if err != nil || seen[x] {
			fail(w, 400, "validation_failed", "Invalid or duplicate category ID")
			return
		}
		seen[x] = true
		ids = append(ids, x)
	}
	owner := principal(r).UserID.String()
	err := a.database.InTx(r.Context(), func(tx context.Context) error {
		if _, err := a.resources.Get(tx, "budgets", principal(r).UserID, id); err != nil {
			return err
		}
		for _, cat := range ids {
			if _, err := a.resources.Get(tx, "categories", principal(r).UserID, cat); err != nil {
				return mongodb.ErrInvalidReference
			}
		}
		if _, err := a.database.DB.Collection("budget_categories").DeleteMany(tx, bson.M{"budget_id": id.String(), "user_id": owner}); err != nil {
			return err
		}
		for _, cat := range ids {
			_, err := a.database.DB.Collection("budget_categories").InsertOne(tx, bson.M{"_id": id.String() + ":" + cat.String(), "budget_id": id.String(), "category_id": cat.String(), "user_id": owner})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.resourceError(w, err)
		return
	}
	respond(w, 200, map[string]any{"budget_id": id, "category_ids": ids})
}

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// financeSnapshot is the current browser document, supplied alongside its
// ciphertext. The server never sees the encryption key. The readable projection
// is the explicit privacy choice required for server-side reports.
type financeSnapshot struct {
	Settings struct {
		Name            string      `json:"name"`
		ExpectedIncome  json.Number `json:"expectedIncome"`
		WarnAt          json.Number `json:"warnAt"`
		AutoLockMinutes int32       `json:"autoLockMinutes"`
		IncomePlan      []struct {
			From   string      `json:"from"`
			Amount json.Number `json:"amount"`
		} `json:"incomePlan"`
		IncomeOverrides map[string]json.Number `json:"incomeOverrides"`
	} `json:"settings"`
	Categories []struct {
		ID, Name, Emoji, Kind string
		Budget                json.Number `json:"budget"`
		Archived              bool
	} `json:"categories"`
	Expenses []struct {
		ID         string
		Date       string
		Amount     json.Number
		CategoryID string `json:"categoryId"`
		Note       string
		LoanID     string `json:"loanId"`
		GoalID     string `json:"goalId"`
		BillID     string `json:"billId"`
	} `json:"expenses"`
	Incomes []struct {
		ID           string
		Date         string
		Amount       json.Number
		Source, Note string
		IsLoan       bool   `json:"isLoan"`
		LoanID       string `json:"loanId"`
	} `json:"incomes"`
	Loans []struct {
		ID, Name, Lender string
		Amount           json.Number
		InterestRate     json.Number `json:"interestRate"`
		MonthlyPayment   json.Number `json:"monthlyPayment"`
		StartDate        string      `json:"startDate"`
		DueDay           int16       `json:"dueDay"`
		Closed           bool
		Note             string
	} `json:"loans"`
	Goals []struct {
		ID, Name, Emoji string
		Target          json.Number
		StartingAmount  json.Number `json:"startingAmount"`
		Deadline, Note  string
	} `json:"goals"`
	Bills []struct {
		ID, Name, CategoryID string
		Amount               json.Number
		DueDay               int16 `json:"dueDay"`
		Months               []int16
		OnlyMonth            string `json:"onlyMonth"`
		FromMonth            string `json:"fromMonth"`
		Overrides            map[string]json.Number
		Skips                []string
		LoanID               string `json:"loanId"`
		GoalID               string `json:"goalId"`
		Active               bool
	} `json:"bills"`
}

func minor(n json.Number) (int64, error) { return scaled(n, 100) }
func scaled(n json.Number, scale int64) (int64, error) {
	if n == "" {
		return 0, nil
	}
	r, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return 0, fmt.Errorf("invalid amount")
	}
	r.Mul(r, new(big.Rat).SetInt64(scale))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("amount must have at most two decimal places and fit in int64")
	}
	return r.Num().Int64(), nil
}
func requiredID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID %q", s)
	}
	return id, nil
}
func optionalID(s string) (any, error) {
	if s == "" {
		return nil, nil
	}
	return requiredID(s)
}
func optionalMonth(s string) (any, error) {
	if s == "" {
		return nil, nil
	}
	m, err := time.Parse("2006-01", s)
	if err != nil {
		return nil, fmt.Errorf("invalid month %q", s)
	}
	return m, nil
}
func optionalDay(d int16) any {
	if d == 0 {
		return nil
	}
	return d
}

func (a *API) replaceSnapshot(ctx context.Context, tx pgx.Tx, uid uuid.UUID, raw json.RawMessage) error {
	var s financeSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("invalid finance snapshot: %w", err)
	}
	if len(s.Categories) > 500 || len(s.Expenses) > 100000 || len(s.Incomes) > 100000 || len(s.Loans) > 1000 || len(s.Goals) > 1000 || len(s.Bills) > 5000 {
		return errors.New("finance snapshot exceeds limits")
	}
	var currency string
	if err := tx.QueryRow(ctx, `SELECT currency FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&currency); err != nil {
		return err
	}
	currency = strings.TrimSpace(currency)
	// Delete dependencies first. API-created rows are outside this projection.
	for _, table := range []string{"expenses", "income", "planned_payments", "budgets", "goals", "loans", "categories"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE user_id=$1 AND sync_origin='vault'", uid); err != nil {
			return err
		}
	}
	expected, err := minor(s.Settings.ExpectedIncome)
	if err != nil || expected < 0 {
		return errors.New("invalid expected income")
	}
	warn, err := scaled(s.Settings.WarnAt, 10000)
	if err != nil || warn < 1000 || warn > 10000 {
		return errors.New("invalid budget warning")
	}
	if s.Settings.AutoLockMinutes < 1 || s.Settings.AutoLockMinutes > 1440 {
		return errors.New("invalid auto lock minutes")
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET name=$2,expected_income_minor=$3,warn_at_bp=$4,auto_lock_minutes=$5,updated_at=now() WHERE id=$1`, uid, s.Settings.Name, expected, warn, s.Settings.AutoLockMinutes); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM income_expectations WHERE user_id=$1`, uid); err != nil {
		return err
	}
	for _, item := range s.Settings.IncomePlan {
		month, err := optionalMonth(item.From)
		if err != nil {
			return err
		}
		amount, err := minor(item.Amount)
		if err != nil || amount < 0 {
			return errors.New("invalid income plan")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO income_expectations(user_id,scope,month,amount_minor) VALUES($1,'from',$2,$3)`, uid, month, amount); err != nil {
			return err
		}
	}
	for key, value := range s.Settings.IncomeOverrides {
		month, err := optionalMonth(key)
		if err != nil {
			return err
		}
		amount, err := minor(value)
		if err != nil || amount < 0 {
			return errors.New("invalid income override")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO income_expectations(user_id,scope,month,amount_minor) VALUES($1,'only',$2,$3)`, uid, month, amount); err != nil {
			return err
		}
	}
	for _, c := range s.Categories {
		id, err := requiredID(c.ID)
		if err != nil {
			return err
		}
		amount, err := minor(c.Budget)
		if err != nil || amount < 0 {
			return errors.New("invalid category budget")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO categories(id,user_id,name,emoji,kind,archived,sync_origin) VALUES($1,$2,$3,$4,$5,$6,'vault')`, id, uid, c.Name, c.Emoji, c.Kind, c.Archived); err != nil {
			return err
		}
		if amount > 0 {
			bid := uuid.NewSHA1(uuid.NameSpaceOID, []byte("finfe-category-budget:"+c.ID))
			if _, err = tx.Exec(ctx, `INSERT INTO budgets(id,user_id,name,period,amount_minor,currency,sync_origin) VALUES($1,$2,$3,'monthly',$4,$5,'vault')`, bid, uid, c.Name, amount, currency); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO budget_categories(budget_id,user_id,category_id) VALUES($1,$2,$3)`, bid, uid, id); err != nil {
				return err
			}
		}
	}
	for _, l := range s.Loans {
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
		start, err := parseDate(l.StartDate)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO loans(id,user_id,name,lender,principal_minor,currency,interest_rate_bp,monthly_payment_minor,start_date,due_day,closed,note,sync_origin) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'vault')`, id, uid, l.Name, l.Lender, amount, currency, rate, payment, start, optionalDay(l.DueDay), l.Closed, l.Note); err != nil {
			return err
		}
	}
	for _, g := range s.Goals {
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
		deadline, err := optionalMonth(g.Deadline)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO goals(id,user_id,name,emoji,target_minor,starting_minor,currency,target_month,note,sync_origin) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'vault')`, id, uid, g.Name, g.Emoji, target, starting, currency, deadline, g.Note); err != nil {
			return err
		}
	}
	for _, b := range s.Bills {
		if b.Months == nil {
			b.Months = []int16{}
		}
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
		if _, err = tx.Exec(ctx, `INSERT INTO planned_payments(id,user_id,name,amount_minor,currency,category_id,due_day,months,only_month,from_month,loan_id,goal_id,active,sync_origin) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'vault')`, id, uid, b.Name, amount, currency, cat, b.DueDay, b.Months, only, from, loan, goal, b.Active); err != nil {
			return err
		}
		for month, n := range b.Overrides {
			m, err := optionalMonth(month)
			if err != nil {
				return err
			}
			amt, err := minor(n)
			if err != nil || amt <= 0 {
				return errors.New("invalid override")
			}
			if _, err = tx.Exec(ctx, `INSERT INTO planned_payment_overrides(planned_payment_id,month,amount_minor) VALUES($1,$2,$3)`, id, m, amt); err != nil {
				return err
			}
		}
		for _, month := range b.Skips {
			m, err := optionalMonth(month)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO planned_payment_skips(planned_payment_id,month) VALUES($1,$2)`, id, m); err != nil {
				return err
			}
		}
	}
	for _, e := range s.Expenses {
		id, err := requiredID(e.ID)
		if err != nil {
			return err
		}
		cat, err := requiredID(e.CategoryID)
		if err != nil {
			return err
		}
		amount, err := minor(e.Amount)
		if err != nil || amount <= 0 {
			return errors.New("invalid expense")
		}
		date, err := parseDate(e.Date)
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
		if _, err = tx.Exec(ctx, `INSERT INTO expenses(id,user_id,occurred_on,amount_minor,currency,category_id,note,loan_id,goal_id,planned_payment_id,sync_origin) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'vault')`, id, uid, date, amount, currency, cat, e.Note, loan, goal, bill); err != nil {
			return err
		}
	}
	for _, i := range s.Incomes {
		id, err := requiredID(i.ID)
		if err != nil {
			return err
		}
		amount, err := minor(i.Amount)
		if err != nil || amount <= 0 {
			return errors.New("invalid income")
		}
		date, err := parseDate(i.Date)
		if err != nil {
			return err
		}
		loan, err := optionalID(i.LoanID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO income(id,user_id,occurred_on,amount_minor,currency,source,note,is_borrowed,loan_id,sync_origin) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'vault')`, id, uid, date, amount, currency, i.Source, i.Note, i.IsLoan, loan); err != nil {
			return err
		}
	}
	return nil
}

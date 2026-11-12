package httpapi

import (
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
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

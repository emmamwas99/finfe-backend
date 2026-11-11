// Package shared holds small helpers used by every application service: IDs, dates,
// tri-state PATCH fields, and translation of database errors into safe client errors.
package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"finfe/internal/domain/calendar"
	"finfe/internal/infrastructure/database"
	apperr "finfe/internal/platform/errors"
)

// Opt is a PATCH field: absent (Set=false), explicit null (Set && Null), or a value.
type Opt[T any] struct {
	Set  bool
	Null bool
	V    T
}

func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.V)
}

// Apply returns the value to store for a non-nullable field.
func (o Opt[T]) Or(cur T) T {
	if o.Set && !o.Null {
		return o.V
	}
	return cur
}

// Ptr returns the value to store for a nullable field.
func (o Opt[T]) Ptr(cur *T) *T {
	if !o.Set {
		return cur
	}
	if o.Null {
		return nil
	}
	v := o.V
	return &v
}

// NewID uses a client-supplied UUID (idempotent creates, offline sync, imports) or makes one.
func NewID(id string) (uuid.UUID, error) {
	if id == "" {
		return uuid.New(), nil
	}
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil {
		return uuid.Nil, apperr.Invalid("validation_failed", "Some fields are invalid.", map[string]string{"id": "must be a UUID"})
	}
	return u, nil
}

// ParseUUIDPtr parses an optional UUID string.
func ParseUUIDPtr(field string, s *string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	u, err := uuid.Parse(*s)
	if err != nil {
		return nil, apperr.Invalid("validation_failed", "Some fields are invalid.", map[string]string{field: "must be a UUID"})
	}
	return &u, nil
}

// ParseDate parses "YYYY-MM-DD" with a field-level error.
func ParseDate(field, s string) (time.Time, error) {
	t, err := calendar.ParseDate(s)
	if err != nil {
		return time.Time{}, apperr.Invalid("validation_failed", "Some fields are invalid.", map[string]string{field: "must be a date (YYYY-MM-DD)"})
	}
	return t, nil
}

// ParseMonthPtr parses an optional "YYYY-MM" into a first-of-month date.
func ParseMonthPtr(field string, s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	m, err := calendar.ParseMonth(*s)
	if err != nil {
		return nil, apperr.Invalid("validation_failed", "Some fields are invalid.", map[string]string{field: "must be a month (YYYY-MM)"})
	}
	t := m.Time()
	return &t, nil
}

func Date(t time.Time) string { return t.Format(time.DateOnly) }

func MonthStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01")
	return &s
}

func DatePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

func UUIDStr(u *uuid.UUID) *string {
	if u == nil {
		return nil
	}
	s := u.String()
	return &s
}

// DB translates database errors into application errors (never leaking SQL details).
func DB(err error, what string) error {
	if err == nil {
		return nil
	}
	if database.IsNoRows(err) {
		return apperr.NotFound(what)
	}
	if code, name, ok := database.Constraint(err); ok {
		switch code {
		case database.UniqueViolation:
			if name == "users_email_key" {
				return apperr.Conflict("email_taken", "An account with this email already exists. Log in instead.")
			}
			return apperr.Conflict("already_exists", fmt.Sprintf("This %s already exists.", what))
		case database.ForeignKeyViolation:
			return apperr.Invalid("invalid_reference", "This refers to a category, loan, goal or account that doesn't exist.", nil)
		case database.CheckViolation:
			return apperr.Invalid("invalid_value", "One of the values is out of range.", map[string]string{"constraint": name})
		}
	}
	return apperr.Internal(err)
}

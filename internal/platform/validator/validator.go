// Package validator collects field errors with a tiny fluent API (no reflection, no tags).
package validator

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	apperr "finfe/internal/platform/errors"
)

type V struct{ fields map[string]string }

func New() *V { return &V{fields: map[string]string{}} }

// Check records msg for field when ok is false (first message per field wins).
func (v *V) Check(ok bool, field, msg string) *V {
	if !ok {
		if _, exists := v.fields[field]; !exists {
			v.fields[field] = msg
		}
	}
	return v
}

func (v *V) Required(field, value string) *V {
	return v.Check(strings.TrimSpace(value) != "", field, "is required")
}

func (v *V) MaxLen(field, value string, n int) *V {
	return v.Check(utf8.RuneCountInString(value) <= n, field, "is too long")
}

func (v *V) Email(field, value string) *V {
	a, err := mail.ParseAddress(value)
	return v.Check(err == nil && a.Address == value && strings.Contains(value, "."), field, "must be a valid email address")
}

func (v *V) Positive(field string, n int64) *V {
	return v.Check(n > 0, field, "must be greater than 0")
}

func (v *V) NonNegative(field string, n int64) *V {
	return v.Check(n >= 0, field, "cannot be negative")
}

func (v *V) OneOf(field, value string, allowed ...string) *V {
	for _, a := range allowed {
		if value == a {
			return v
		}
	}
	return v.Check(false, field, "must be one of: "+strings.Join(allowed, ", "))
}

// Err returns a validation error, or nil when every check passed.
func (v *V) Err() error {
	if len(v.fields) == 0 {
		return nil
	}
	return apperr.Invalid("validation_failed", "Some fields are invalid.", v.fields)
}

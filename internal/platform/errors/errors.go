// Package errors defines application errors that map cleanly onto HTTP responses.
// Messages are safe to show to users; internal causes are logged, never returned.
package errors

import (
	stderrors "errors"
	"fmt"
)

type Kind int

const (
	KindInternal Kind = iota
	KindInvalid
	KindUnauthenticated
	KindForbidden
	KindNotFound
	KindConflict
	KindRateLimited
	KindUnavailable
)

// Error is an application error.
type Error struct {
	Kind    Kind
	Code    string            // stable machine-readable code, e.g. "validation_failed"
	Message string            // human-readable, safe for clients
	Fields  map[string]string // per-field validation messages
	Err     error             // internal cause (logged only)
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

func Invalid(code, msg string, fields map[string]string) *Error {
	return &Error{Kind: KindInvalid, Code: code, Message: msg, Fields: fields}
}
func Unauthenticated(msg string) *Error {
	return &Error{Kind: KindUnauthenticated, Code: "unauthenticated", Message: msg}
}
func Forbidden(code, msg string) *Error { return &Error{Kind: KindForbidden, Code: code, Message: msg} }
func NotFound(what string) *Error {
	return &Error{Kind: KindNotFound, Code: "not_found", Message: what + " not found"}
}
func Conflict(code, msg string) *Error { return &Error{Kind: KindConflict, Code: code, Message: msg} }
func RateLimited() *Error {
	return &Error{Kind: KindRateLimited, Code: "rate_limited", Message: "Too many requests. Please wait a moment and try again."}
}
func Internal(err error) *Error {
	return &Error{Kind: KindInternal, Code: "internal_error", Message: "Something went wrong. Please try again.", Err: err}
}
func Unavailable(msg string, err error) *Error {
	return &Error{Kind: KindUnavailable, Code: "unavailable", Message: msg, Err: err}
}

// As extracts an *Error; anything else becomes an internal error.
func As(err error) *Error {
	var e *Error
	if stderrors.As(err, &e) {
		return e
	}
	return Internal(err)
}

// Is re-exports errors.Is for convenience.
func Is(err, target error) bool { return stderrors.Is(err, target) }

// Package fault defines safe errors shared by providers and orchestration.
package fault

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Kind string

const (
	InvalidInput   Kind = "invalid input"
	Authentication Kind = "authentication unavailable"
	Permission     Kind = "permission denied"
	Verification   Kind = "verification required"
	RateLimited    Kind = "rate limited"
	Upstream       Kind = "upstream unavailable"
	Unsupported    Kind = "unsupported operation"
	Schema         Kind = "schema mismatch"
	Partial        Kind = "partial response"
	Network        Kind = "network failure"
	Storage        Kind = "local storage failure"
	Budget         Kind = "request budget exhausted"
	Download       Kind = "download failure"
	Unavailable    Kind = "media unavailable"
)

// Detail must be a local diagnostic, never an upstream message, URL or body.
type Error struct {
	Kind       Kind
	Op         string
	Detail     string
	StatusCode int
	RetryAfter time.Duration
	Cause      error
}

func (e *Error) Error() string {
	s := fmt.Sprintf("%s: %s", e.Op, e.Kind)
	if e.Detail != "" {
		s += " (" + e.Detail + ")"
	}
	if e.RetryAfter > 0 {
		s += fmt.Sprintf("; retry after %s", e.RetryAfter)
	}
	return s
}
func (e *Error) Unwrap() error               { return e.Cause }
func New(kind Kind, op, detail string) error { return &Error{Kind: kind, Op: op, Detail: detail} }

// Is also searches joined errors; a partial result must never hide a denial.
func Is(err error, kind Kind) bool {
	if e, ok := err.(*Error); ok && e.Kind == kind {
		return true
	}
	if e, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range e.Unwrap() {
			if Is(child, kind) {
				return true
			}
		}
	}
	if e, ok := err.(interface{ Unwrap() error }); ok {
		return Is(e.Unwrap(), kind)
	}
	return false
}
func Global(err error) bool {
	// An HTTP client timeout also matches DeadlineExceeded. The runner checks
	// its own context to distinguish its deadline from a single slow request.
	return Is(err, Authentication) || Is(err, Permission) || Is(err, Verification) || Is(err, RateLimited) || Is(err, Budget) || errors.Is(err, context.Canceled)
}

// OnlyPartial excludes joined validation or access failures from usable data.
func OnlyPartial(err error) bool {
	if e, ok := err.(*Error); ok {
		return e.Kind == Partial && (e.Cause == nil || OnlyPartial(e.Cause))
	}
	if e, ok := err.(interface{ Unwrap() []error }); ok {
		children := e.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !OnlyPartial(child) {
				return false
			}
		}
		return true
	}
	if e, ok := err.(interface{ Unwrap() error }); ok {
		return OnlyPartial(e.Unwrap())
	}
	return false
}

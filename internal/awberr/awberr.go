// Package awberr carries the classification that both of awb's surfaces
// report. A failure is one of four kinds, and each kind has exactly one exit
// code on the command line and one status code over HTTP, so the CLI and the
// API can never disagree about what went wrong.
//
// The mapping is:
//
//	Kind      exit  status
//	Runtime      1     500
//	Usage        2     400
//	NotFound     3     404
//	Conflict     4     409
package awberr

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind classifies a failure. The zero value is Runtime, so an error that has
// not been classified is reported as one rather than as a usage mistake.
type Kind int

const (
	// Runtime is a failure that is nobody's fault in particular: the database is
	// unreachable, a file cannot be read, a transaction timed out.
	Runtime Kind = iota
	// Usage is a mistake in what the caller asked for: an unknown enum value, a
	// malformed label, mutually exclusive flags, text failing the input rules.
	Usage
	// NotFound is an entity addressed by name that does not exist.
	NotFound
	// Conflict is a constraint that depends on stored state: a dependency cycle,
	// a duplicate, a claim held by somebody else, a parent already set.
	Conflict
)

// ExitCode is the process exit status for a failure of this kind.
func (k Kind) ExitCode() int {
	switch k {
	case Usage:
		return 2
	case NotFound:
		return 3
	case Conflict:
		return 4
	case Runtime:
		return 1
	default:
		return 1
	}
}

// HTTPStatus is the response status for a failure of this kind.
func (k Kind) HTTPStatus() int {
	switch k {
	case Usage:
		return http.StatusBadRequest
	case NotFound:
		return http.StatusNotFound
	case Conflict:
		return http.StatusConflict
	case Runtime:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// KindFromHTTPStatus inverts HTTPStatus for the remote backend. The statuses
// that no kind maps onto — 401, 403, 405, 412, 413, 415 and anything else —
// become Runtime, which is exit code 1: they say something about how the
// client behaved rather than about what it asked for, and the command line has
// no separate code for that.
func KindFromHTTPStatus(status int) Kind {
	switch status {
	case http.StatusBadRequest:
		return Usage
	case http.StatusNotFound:
		return NotFound
	case http.StatusConflict:
		return Conflict
	default:
		return Runtime
	}
}

// Error is a classified failure carrying a human-readable message. The message
// is what the user or the API client sees; the kind is the machine-readable
// half.
type Error struct {
	Kind Kind
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Msg == "" && e.Err != nil {
		return e.Err.Error()
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// Usagef reports a mistake in what the caller asked for (exit 2, 400).
func Usagef(format string, a ...any) *Error {
	return &Error{Kind: Usage, Msg: fmt.Sprintf(format, a...)}
}

// NotFoundf reports an entity that does not exist (exit 3, 404).
func NotFoundf(format string, a ...any) *Error {
	return &Error{Kind: NotFound, Msg: fmt.Sprintf(format, a...)}
}

// Conflictf reports a constraint on stored state (exit 4, 409).
func Conflictf(format string, a ...any) *Error {
	return &Error{Kind: Conflict, Msg: fmt.Sprintf(format, a...)}
}

// Runtimef reports a failure that is not the caller's mistake (exit 1, 500).
func Runtimef(format string, a ...any) *Error {
	return &Error{Kind: Runtime, Msg: fmt.Sprintf(format, a...)}
}

// Wrap classifies err, keeping it in the chain for errors.Is and errors.As. It
// returns nil when err is nil, so it can be applied to a result directly.
func Wrap(kind Kind, err error, format string, a ...any) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, a...) + ": " + err.Error(), Err: err}
}

// KindOf returns the kind of err, or Runtime when err carries no
// classification. An unclassified failure is a runtime one, never a usage one:
// blaming the caller for a bug would be the worse mistake.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return Runtime
}

// ExitCode is the process exit status for err, and 0 when err is nil.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return KindOf(err).ExitCode()
}

// ErrPreconditionFailed marks a conditional request whose If-Match no longer
// matches. It has no exit code of its own — the command line never sends
// If-Match — so it is a Runtime error that the HTTP adapter recognises and
// answers 412.
var ErrPreconditionFailed = errors.New("it has changed since you read it")

// PreconditionFailed reports that a conditional request lost its race.
func PreconditionFailed(what string) error {
	return &Error{
		Kind: Runtime,
		Msg:  what + " has changed since it was read",
		Err:  ErrPreconditionFailed,
	}
}

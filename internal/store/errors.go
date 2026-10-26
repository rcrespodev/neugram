package store

import (
	"errors"
	"fmt"
)

// Error carries one of the stable codes of the error catalog in docs/specs/api.md.
type Error struct {
	Code    string
	Message string
	Hint    string
	Details map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Is matches on Code, so errors.Is(err, ErrNotFound) holds for any not_found.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

var (
	ErrNotFound       = &Error{Code: "not_found"}
	ErrInvalidRequest = &Error{Code: "invalid_request"}
	ErrInvalidType    = &Error{Code: "invalid_type"}
	ErrInvalidEnum    = &Error{Code: "invalid_enum"}
	ErrUnknownSession = &Error{Code: "unknown_session"}
	ErrNoPin          = &Error{Code: "no_pin"}
	ErrTaskExists     = &Error{Code: "task_exists"}
	ErrInvalidParent  = &Error{Code: "invalid_parent"}
	ErrUnknownProduct = &Error{Code: "unknown_product"}
)

func isCode(err error, kind *Error) bool { return errors.Is(err, kind) }

func errorf(kind *Error, format string, args ...any) error {
	return &Error{Code: kind.Code, Message: fmt.Sprintf(format, args...)}
}

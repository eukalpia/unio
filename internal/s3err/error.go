package s3err

import "fmt"

// Error is a typed error carrying an S3 error code. Message is safe to show
// to clients. Err is the internal cause; it is logged, never returned to
// clients.
type Error struct {
	Code    Code
	Message string
	Err     error
}

// Errorf returns an *Error with the given code and a client-safe message.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap returns an *Error with the given code wrapping an internal cause.
// The message shown to clients is the generic one for the code.
func Wrap(code Code, err error) *Error {
	return &Error{Code: code, Err: err}
}

func (e *Error) Error() string {
	switch {
	case e.Message != "" && e.Err != nil:
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	case e.Message != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	default:
		return string(e.Code)
	}
}

// Unwrap returns the internal cause.
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is an *Error with the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// S3Code implements Coder.
func (e *Error) S3Code() Code { return e.Code }

package policy

import (
	"fmt"

	"github.com/eukalpia/unio/internal/s3err"
)

// Error is a policy document failure: malformed JSON, an unknown or
// unsupported element, or a value outside the accepted form. S3 reports every
// such failure as MalformedPolicy, and the message names the element at
// fault. Error implements s3err.Coder, so the S3 layer reports it correctly.
type Error struct {
	// Msg describes the problem. It names document elements, never values
	// supplied by the caller beyond a bounded prefix.
	Msg string
}

// Error implements error.
func (e *Error) Error() string {
	return "policy: " + e.Msg
}

// S3Code implements s3err.Coder.
func (e *Error) S3Code() s3err.Code {
	return s3err.MalformedPolicy
}

// invalid returns the error for a document that fails validation.
func invalid(format string, args ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, args...)}
}

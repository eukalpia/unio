package policy

import (
	"fmt"

	"github.com/eukalpia/unio/internal/s3err"
)

// Error is a policy validation failure. It implements s3err.Coder, so the
// S3 layer can report it with the right code.
type Error struct {
	// Code is InvalidArgument for malformed documents and NotImplemented for
	// elements this version does not support.
	Code s3err.Code
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
	return e.Code
}

// invalid returns an InvalidArgument error for a malformed document.
func invalid(format string, args ...any) *Error {
	return &Error{Code: s3err.InvalidArgument, Msg: fmt.Sprintf(format, args...)}
}

// unsupported returns a NotImplemented error for an element this version
// does not support.
func unsupported(format string, args ...any) *Error {
	return &Error{Code: s3err.NotImplemented, Msg: fmt.Sprintf(format, args...)}
}

package s3err

import (
	"context"
	"errors"
)

// CodeOf returns the S3 error code for err: the code of the outermost Coder
// in its chain, RequestTimeout for context errors, InternalError otherwise.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var c Coder
	if errors.As(err, &c) {
		return c.S3Code()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return RequestTimeout
	}
	return InternalError
}

// Is reports whether err carries the given S3 error code.
func Is(err error, code Code) bool { return err != nil && CodeOf(err) == code }

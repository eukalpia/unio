// Package syncerr marks failures of fsync-class operations: fsync, fdatasync,
// directory sync and the index's write-ahead-log sync.
//
// A failed fsync is never retried. The kernel may have dropped the dirty pages
// it could not write, so a later fsync can succeed without the data being on
// disk (the 2018 "fsyncgate" problem). The drive therefore treats the first
// such failure as fatal to its writes. Layers that run a sync wrap its error
// with Wrap, and the drive checks for the marker with Is.
package syncerr

import (
	"errors"
	"fmt"
)

// Error is a failed fsync-class operation.
type Error struct {
	// Op names the operation, for example "fdatasync" or "fsync dir".
	Op string
	// Path is the file or directory the operation was applied to.
	Path string
	// Err is the underlying error.
	Err error
}

// Error implements error.
func (e *Error) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Op, e.Path, e.Err)
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error {
	return e.Err
}

// Wrap marks err as a failed operation op on path. It returns nil when err is
// nil, so a sync can be wrapped without a separate check.
func Wrap(op, path string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Op: op, Path: path, Err: err}
}

// Is reports whether err is, or wraps, a failed fsync-class operation.
func Is(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

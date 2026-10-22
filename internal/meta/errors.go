package meta

import "github.com/eukalpia/unio/internal/s3err"

// Errors returned by Store and SystemKV implementations.
var (
	// ErrNotLeader is returned by a replica that is not its group's leader.
	ErrNotLeader = s3err.NewSentinel("meta: not the leader", s3err.ServiceUnavailable)
	// ErrRevisionMismatch is returned by compare-and-swap operations.
	ErrRevisionMismatch = s3err.NewSentinel("meta: revision mismatch", s3err.ConditionalRequestConflict)
	// ErrNotFound is returned by SystemKV for missing keys.
	ErrNotFound = s3err.NewSentinel("meta: not found", s3err.InternalError)
	// ErrValueTooLarge is returned for SystemKV values over the limit.
	ErrValueTooLarge = s3err.NewSentinel("meta: value too large", s3err.InvalidArgument)
)

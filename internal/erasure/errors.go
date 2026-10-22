package erasure

import "github.com/eukalpia/unio/internal/s3err"

// Errors returned by Set implementations.
var (
	// ErrWriteQuorum is returned when fewer drives than the write quorum
	// acknowledged a write.
	ErrWriteQuorum = s3err.NewSentinel("erasure: write quorum not reached", s3err.ServiceUnavailable)
	// ErrReadQuorum is returned when fewer shards than needed could be read.
	ErrReadQuorum = s3err.NewSentinel("erasure: read quorum not reached", s3err.ServiceUnavailable)
	// ErrCorrupt is returned when data fails bitrot verification and cannot
	// be reconstructed.
	ErrCorrupt = s3err.NewSentinel("erasure: data corrupt", s3err.InternalError)
	// ErrUnknownSet is returned by a Registry for an unknown set id.
	ErrUnknownSet = s3err.NewSentinel("erasure: unknown set", s3err.InternalError)
)

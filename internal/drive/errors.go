package drive

import "github.com/eukalpia/unio/internal/s3err"

// Errors returned by Drive implementations.
var (
	// ErrShardNotFound is returned for a shard the drive does not hold.
	ErrShardNotFound = s3err.NewSentinel("drive: shard not found", s3err.InternalError)
	// ErrShardExists is returned when writing an id that already exists.
	// Shards are immutable.
	ErrShardExists = s3err.NewSentinel("drive: shard already exists", s3err.InternalError)
	// ErrOffline is returned when a drive cannot be reached.
	ErrOffline = s3err.NewSentinel("drive: offline", s3err.ServiceUnavailable)
	// ErrFaulty is returned when a drive reports I/O errors.
	ErrFaulty = s3err.NewSentinel("drive: faulty", s3err.InternalError)
	// ErrCorrupt is returned when on-disk structures fail verification.
	ErrCorrupt = s3err.NewSentinel("drive: data corrupt", s3err.InternalError)
	// ErrClosed is returned after Close.
	ErrClosed = s3err.NewSentinel("drive: closed", s3err.ServiceUnavailable)
)

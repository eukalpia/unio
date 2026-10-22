package group

import "github.com/eukalpia/unio/internal/s3err"

// Errors returned by a group.
var (
	// ErrBusy is returned when a proposal could not be queued because the
	// leader's apply queue stayed full. The command was not appended.
	ErrBusy = s3err.NewSentinel("meta: apply queue full", s3err.ServiceUnavailable)
	// ErrOutcomeUnknown is returned when a command may or may not have been
	// applied, for example because leadership was lost after the command was
	// appended. Clients must treat the write as possibly applied.
	ErrOutcomeUnknown = s3err.NewSentinel("meta: write outcome unknown", s3err.ServiceUnavailable)
)

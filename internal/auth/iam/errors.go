package iam

import (
	"errors"

	"github.com/eukalpia/unio/internal/s3err"
)

// Errors returned by management operations. Each carries the S3 code that
// the admin API reports. s3err has no entity-specific codes, so the closest
// standard codes are used: NoSuchKey for missing entities and
// ConditionalRequestConflict for duplicates.
var (
	// ErrUserExists is returned when creating a user whose name is taken.
	ErrUserExists = s3err.NewSentinel("iam: user already exists", s3err.ConditionalRequestConflict)
	// ErrUserNotFound is returned for operations on a missing user.
	ErrUserNotFound = s3err.NewSentinel("iam: user not found", s3err.NoSuchKey)
	// ErrAccessKeyNotFound is returned for operations on a missing key.
	ErrAccessKeyNotFound = s3err.NewSentinel("iam: access key not found", s3err.NoSuchKey)
	// ErrInvalidUserName is returned for a user name outside the allowed form.
	ErrInvalidUserName = s3err.NewSentinel("iam: invalid user name", s3err.InvalidArgument)
	// ErrTooManyRetries is returned when compare-and-swap keeps losing to
	// concurrent writers. The request is safe to retry.
	ErrTooManyRetries = s3err.NewSentinel("iam: too many concurrent updates, retry", s3err.ServiceUnavailable)
)

// errRecordVersion reports a stored record whose version this build does not
// read. It is an internal error.
var errRecordVersion = errors.New("iam: unsupported record version")

package object

import (
	"time"

	"github.com/eukalpia/unio/internal/ids"
)

// VersioningState is the versioning configuration of a bucket.
type VersioningState uint8

// Bucket versioning states.
const (
	VersioningUnversioned VersioningState = iota
	VersioningEnabled
	VersioningSuspended
)

// BucketInfo describes a bucket.
type BucketInfo struct {
	Name       string
	ID         ids.BucketID
	Created    time.Time
	Owner      string
	Versioning VersioningState
}

// MakeBucketOptions configures bucket creation.
type MakeBucketOptions struct {
	Owner      string
	Versioning VersioningState
}

// DeleteBucketOptions configures bucket deletion.
type DeleteBucketOptions struct {
	// Force deletes a non-empty bucket and all of its objects. Admin only.
	Force bool
}

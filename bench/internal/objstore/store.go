// Package objstore defines the small S3 surface used by the benchmark and an
// implementation backed by the AWS SDK for Go v2. Any S3-compatible endpoint
// works, so Unio and MinIO can be measured with the same workloads.
package objstore

import "context"

// Store is the subset of the S3 API that the benchmark uses. Implementations
// must be safe for concurrent use by many goroutines.
type Store interface {
	// EnsureBucket creates bucket if it does not exist.
	EnsureBucket(ctx context.Context, bucket string) error
	// PutObject stores body under key. The body is read but not retained.
	PutObject(ctx context.Context, bucket, key string, body []byte) error
	// GetObject reads the whole object, discards the body and returns its
	// length in bytes.
	GetObject(ctx context.Context, bucket, key string) (int64, error)
	// ListObjectsV2 returns one page of a listing.
	ListObjectsV2(ctx context.Context, bucket string, req ListRequest) (ListPage, error)
}

// ListRequest holds the parameters of one ListObjectsV2 call. Empty Prefix,
// Delimiter and ContinuationToken are omitted from the request. A zero MaxKeys
// leaves the server default in place.
type ListRequest struct {
	Prefix            string
	Delimiter         string
	MaxKeys           int32
	ContinuationToken string
}

// ListPage describes one page of a ListObjectsV2 response. Keys and
// CommonPrefixes are counts, not contents; the benchmark does not need the
// names.
type ListPage struct {
	// Keys is the number of objects in the page.
	Keys int
	// CommonPrefixes is the number of delimiter groups in the page.
	CommonPrefixes int
	// Truncated reports whether more pages follow.
	Truncated bool
	// NextToken is the continuation token for the next page.
	NextToken string
}

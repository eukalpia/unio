package core

import (
	"context"
	"io"
)

// Body is a verified request payload. It is produced by the S3 front end
// (internal/hashreader) and consumed by the object layer.
//
// Contract:
//   - Read yields exactly Size() bytes and then io.EOF.
//   - If any integrity check fails (Content-MD5, x-amz-content-sha256,
//     x-amz-checksum-*, aws-chunked signatures, length), Read returns an
//     error instead of io.EOF. The final Read may return the error together
//     with the last bytes.
//   - MD5 and Checksum are valid only after Read has returned io.EOF.
//
// Consumers must not commit anything that depends on the payload unless
// Read returned io.EOF (invariant I4).
type Body interface {
	io.Reader
	// Size is the exact decoded payload length.
	Size() int64
	// MD5 is the digest of the bytes read; the object ETag for single-part
	// uploads.
	MD5() [16]byte
	// Checksum is the additional checksum computed over the bytes read for
	// the algorithm the client requested, or a zero Checksum.
	Checksum() Checksum
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

// PutObjectOptions carries the attributes of a new object version.
type PutObjectOptions struct {
	ContentType        string
	ContentEncoding    string
	ContentDisposition string
	ContentLanguage    string
	CacheControl       string
	Expires            string
	UserMetadata       map[string]string
	Tags               string
	StorageClass       string
	// ChecksumAlgorithm is the additional checksum the client asked for.
	ChecksumAlgorithm ChecksumAlgorithm
	// Conditions are If-Match / If-None-Match for conditional writes.
	Conditions Conditions
}

// GetObjectOptions configures reads.
type GetObjectOptions struct {
	VersionID  string
	Range      *HTTPRange
	PartNumber int
	Conditions Conditions
}

// ObjectReader streams object content. Callers must Close it.
type ObjectReader struct {
	io.ReadCloser
	Info ObjectInfo
	// Offset and Length describe the window being served; for a full read
	// Offset is 0 and Length is Info.Size.
	Offset int64
	Length int64
}

// DeleteObjectOptions configures deletion.
type DeleteObjectOptions struct {
	VersionID  string
	Conditions Conditions
}

// ObjectToDelete names one object in a DeleteObjects call.
type ObjectToDelete struct {
	Key       string
	VersionID string
}

// DeleteResult reports the outcome of deleting one object.
type DeleteResult struct {
	Key                   string
	VersionID             string
	DeleteMarker          bool
	DeleteMarkerVersionID string
	// Err is nil on success. Deleting a missing key is a success in S3.
	Err error
}

// ObjectRef names an object version, used as a copy source.
type ObjectRef struct {
	Bucket    string
	Key       string
	VersionID string
}

// MetadataDirective controls metadata handling in CopyObject.
type MetadataDirective uint8

// Metadata directives.
const (
	MetadataCopy MetadataDirective = iota
	MetadataReplace
)

// CopyObjectOptions configures CopyObject and UploadPartCopy.
type CopyObjectOptions struct {
	// SourceConditions are the x-amz-copy-source-if-* preconditions.
	SourceConditions Conditions
	// SourceRange is used by UploadPartCopy.
	SourceRange *HTTPRange
	Directive   MetadataDirective
	// Put holds the destination attributes when Directive is
	// MetadataReplace, and destination conditions in all cases.
	Put PutObjectOptions
}

// ListOptions configures object listing.
type ListOptions struct {
	Prefix    string
	Delimiter string
	// After is the exclusive start key: listing returns keys (or common
	// prefixes) strictly greater than After.
	After string
	// MaxKeys is the page size, 1..MaxListKeys. Zero means MaxListKeys.
	MaxKeys int
}

// ListResult is one page of a listing.
type ListResult struct {
	Objects        []ObjectInfo
	CommonPrefixes []string
	IsTruncated    bool
	// NextAfter is the value to pass as ListOptions.After for the next page.
	// It is the last key or common prefix returned.
	NextAfter string
}

// PartInfo describes an uploaded multipart part.
type PartInfo struct {
	PartNumber int
	Size       int64
	ETag       string
	ModTime    int64 // unix nanoseconds
	Checksum   Checksum
}

// CompletePart is one entry of a CompleteMultipartUpload request.
type CompletePart struct {
	PartNumber int
	ETag       string
	Checksum   Checksum
}

// ListPartsOptions configures ListParts.
type ListPartsOptions struct {
	PartNumberMarker int
	MaxParts         int
}

// ListPartsResult is a page of parts.
type ListPartsResult struct {
	Parts                []PartInfo
	IsTruncated          bool
	NextPartNumberMarker int
	ChecksumAlgorithm    ChecksumAlgorithm
}

// MultipartUpload describes an in-progress upload.
type MultipartUpload struct {
	Key       string
	UploadID  string
	Initiated int64 // unix nanoseconds
}

// ListUploadsOptions configures ListMultipartUploads.
type ListUploadsOptions struct {
	Prefix         string
	Delimiter      string
	KeyMarker      string
	UploadIDMarker string
	MaxUploads     int
}

// ListUploadsResult is a page of multipart uploads.
type ListUploadsResult struct {
	Uploads            []MultipartUpload
	CommonPrefixes     []string
	IsTruncated        bool
	NextKeyMarker      string
	NextUploadIDMarker string
}

// ObjectLayer is the storage API used by the S3 front end. Implemented by
// internal/object.
//
// All methods validate bucket names, keys and limits from limits.go and
// return *Error values with S3 codes for client errors. Implementations are
// safe for concurrent use.
type ObjectLayer interface {
	MakeBucket(ctx context.Context, bucket string, opts MakeBucketOptions) (BucketInfo, error)
	GetBucketInfo(ctx context.Context, bucket string) (BucketInfo, error)
	ListBuckets(ctx context.Context) ([]BucketInfo, error)
	DeleteBucket(ctx context.Context, bucket string, opts DeleteBucketOptions) error

	// PutObject stores body as the new current version of bucket/key. The
	// version becomes visible only after body was read to io.EOF and the data
	// is durable on a write quorum (I1, I4).
	PutObject(ctx context.Context, bucket, key string, body Body, opts PutObjectOptions) (ObjectInfo, error)
	// GetObject evaluates opts.Conditions and opts.Range and returns a reader
	// that verifies every block it serves (I2).
	GetObject(ctx context.Context, bucket, key string, opts GetObjectOptions) (*ObjectReader, error)
	HeadObject(ctx context.Context, bucket, key string, opts GetObjectOptions) (ObjectInfo, error)
	DeleteObject(ctx context.Context, bucket, key string, opts DeleteObjectOptions) (DeleteResult, error)
	// DeleteObjects deletes up to MaxDeleteObjects keys; per-key failures are
	// reported in the results, the error is for request-level failures.
	DeleteObjects(ctx context.Context, bucket string, objects []ObjectToDelete) ([]DeleteResult, error)
	CopyObject(ctx context.Context, src ObjectRef, dstBucket, dstKey string, opts CopyObjectOptions) (ObjectInfo, error)
	ListObjects(ctx context.Context, bucket string, opts ListOptions) (ListResult, error)

	NewMultipartUpload(ctx context.Context, bucket, key string, opts PutObjectOptions) (uploadID string, err error)
	PutObjectPart(ctx context.Context, bucket, key, uploadID string, partNumber int, body Body) (PartInfo, error)
	CopyObjectPart(ctx context.Context, src ObjectRef, bucket, key, uploadID string, partNumber int, opts CopyObjectOptions) (PartInfo, error)
	ListParts(ctx context.Context, bucket, key, uploadID string, opts ListPartsOptions) (ListPartsResult, error)
	// CompleteMultipartUpload atomically turns the listed parts into the
	// current version. opts carries destination conditions only.
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []CompletePart, opts PutObjectOptions) (ObjectInfo, error)
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
	ListMultipartUploads(ctx context.Context, bucket string, opts ListUploadsOptions) (ListUploadsResult, error)
}

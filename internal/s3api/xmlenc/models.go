package xmlenc

import "time"

// Owner identifies an account.
type Owner struct {
	// ID is the canonical user identifier.
	ID string
	// DisplayName is the human-readable account name.
	DisplayName string
}

// Content is one object entry in a listing. Owner is emitted only when
// non-nil. ETag is emitted exactly as given; callers pass it quoted, for
// example "\"abc\"". ChecksumAlgorithm is emitted only when non-empty.
type Content struct {
	// Key is the object key.
	Key string
	// ETag is the entity tag, already quoted.
	ETag string
	// StorageClass is the storage class name.
	StorageClass string
	// LastModified is the last modification time of the object.
	LastModified time.Time
	// Size is the object size in bytes.
	Size int64
	// Owner is the object owner, or nil to omit the element.
	Owner *Owner
	// ChecksumAlgorithm is the checksum algorithm name, or empty to omit it.
	ChecksumAlgorithm string
}

// ListObjectsV2Result is a ListObjectsV2 response.
//
// Delimiter, StartAfter, ContinuationToken, NextContinuationToken and
// EncodingType are emitted only when non-empty. Key-like values are
// percent-encoded when EncodingType is "url"; continuation tokens are always
// escaped as text.
type ListObjectsV2Result struct {
	// Name is the bucket name.
	Name string
	// Prefix is the listing prefix.
	Prefix string
	// Delimiter is the grouping delimiter, if any.
	Delimiter string
	// StartAfter is the key the listing started after, if any.
	StartAfter string
	// ContinuationToken is the token supplied by the request, if any.
	ContinuationToken string
	// NextContinuationToken is the token for the next page, if any.
	NextContinuationToken string
	// EncodingType is "url" when key-like values are percent-encoded.
	EncodingType string
	// MaxKeys is the maximum number of keys requested.
	MaxKeys int
	// KeyCount is the number of keys and common prefixes returned.
	KeyCount int
	// IsTruncated reports whether more results are available.
	IsTruncated bool
	// Contents lists the matching objects.
	Contents []Content
	// CommonPrefixes lists the rolled-up common prefixes.
	CommonPrefixes []string
}

// ListObjectsV1Result is a ListObjects (V1) response.
//
// Delimiter, NextMarker and EncodingType are emitted only when non-empty.
// Key-like values are percent-encoded when EncodingType is "url".
type ListObjectsV1Result struct {
	// Name is the bucket name.
	Name string
	// Prefix is the listing prefix.
	Prefix string
	// Marker is the marker supplied by the request.
	Marker string
	// Delimiter is the grouping delimiter, if any.
	Delimiter string
	// NextMarker is the marker for the next page, if any.
	NextMarker string
	// EncodingType is "url" when key-like values are percent-encoded.
	EncodingType string
	// MaxKeys is the maximum number of keys requested.
	MaxKeys int
	// IsTruncated reports whether more results are available.
	IsTruncated bool
	// Contents lists the matching objects.
	Contents []Content
	// CommonPrefixes lists the rolled-up common prefixes.
	CommonPrefixes []string
}

// Bucket describes one bucket in a ListBuckets response.
type Bucket struct {
	// Name is the bucket name.
	Name string
	// CreationDate is the time the bucket was created.
	CreationDate time.Time
}

// ListBucketsResult is a ListAllMyBuckets response.
type ListBucketsResult struct {
	// Owner is the account that owns the buckets.
	Owner Owner
	// Buckets lists the buckets of the account.
	Buckets []Bucket
}

// ErrorResponse is the body of every S3 error. Fields are always emitted, even
// when empty.
type ErrorResponse struct {
	// Code is the S3 error code.
	Code string
	// Message is the client-safe error message.
	Message string
	// Resource is the resource the error refers to.
	Resource string
	// RequestID identifies the request.
	RequestID string
	// HostID identifies the host that served the request.
	HostID string
}

// Checksums holds base64 additional-checksum values. Each element is emitted
// only when non-empty.
type Checksums struct {
	// CRC32 is the base64 CRC-32 checksum.
	CRC32 string
	// CRC32C is the base64 CRC-32C checksum.
	CRC32C string
	// CRC64NVME is the base64 CRC-64/NVME checksum.
	CRC64NVME string
	// SHA1 is the base64 SHA-1 checksum.
	SHA1 string
	// SHA256 is the base64 SHA-256 checksum.
	SHA256 string
}

// InitiateMultipartUploadResult is an InitiateMultipartUpload response.
type InitiateMultipartUploadResult struct {
	// Bucket is the bucket that holds the upload.
	Bucket string
	// Key is the object key being uploaded.
	Key string
	// UploadID identifies the multipart upload.
	UploadID string
}

// CompleteMultipartUploadResult is a CompleteMultipartUpload response.
// ChecksumType is emitted only when non-empty.
type CompleteMultipartUploadResult struct {
	// Location is the URL of the completed object.
	Location string
	// Bucket is the bucket that holds the object.
	Bucket string
	// Key is the object key.
	Key string
	// ETag is the entity tag of the completed object, already quoted.
	ETag string
	// ChecksumType is COMPOSITE or FULL_OBJECT, if set.
	ChecksumType string
	// Checksums holds the additional checksums of the object.
	Checksums
}

// CopyObjectResult is a CopyObject response. ChecksumType is emitted only when
// non-empty.
type CopyObjectResult struct {
	// ETag is the entity tag of the new object, already quoted.
	ETag string
	// LastModified is the time the copy was written.
	LastModified time.Time
	// ChecksumType is COMPOSITE or FULL_OBJECT, if set.
	ChecksumType string
	// Checksums holds the additional checksums of the new object.
	Checksums
}

// DeletedObject describes one successfully deleted object in a DeleteResult.
// DeleteMarker and DeleteMarkerVersionID are emitted only when DeleteMarker is
// true. VersionId is emitted only when VersionID is non-empty.
type DeletedObject struct {
	// Key is the object key.
	Key string
	// VersionID is the version that was deleted, if any.
	VersionID string
	// DeleteMarkerVersionID is the version of the delete marker created.
	DeleteMarkerVersionID string
	// DeleteMarker reports whether the deletion created or removed a marker.
	DeleteMarker bool
}

// DeleteError describes one failed deletion in a DeleteResult. VersionId is
// emitted only when VersionID is non-empty.
type DeleteError struct {
	// Key is the object key.
	Key string
	// VersionID is the version that failed to delete, if any.
	VersionID string
	// Code is the S3 error code.
	Code string
	// Message is the client-safe error message.
	Message string
}

// DeleteResult is a DeleteObjects response. Deleted is empty in Quiet mode.
//
// The result has no encoding-type field, so keys in it are always escaped as
// text.
type DeleteResult struct {
	// Deleted lists the objects that were deleted.
	Deleted []DeletedObject
	// Errors lists the objects that could not be deleted.
	Errors []DeleteError
}

// Part describes one uploaded part in a ListParts response. Non-empty
// checksum elements are emitted.
type Part struct {
	// PartNumber is the part number, starting at 1.
	PartNumber int
	// LastModified is the time the part was uploaded.
	LastModified time.Time
	// ETag is the entity tag of the part, already quoted.
	ETag string
	// Size is the part size in bytes.
	Size int64
	// Checksums holds the additional checksums of the part.
	Checksums
}

// ListPartsResult is a ListParts response. Owner and Initiator are both
// emitted with the same value only when Owner is non-nil.
type ListPartsResult struct {
	// Bucket is the bucket that holds the upload.
	Bucket string
	// Key is the object key being uploaded.
	Key string
	// UploadID identifies the multipart upload.
	UploadID string
	// StorageClass is the storage class of the upload.
	StorageClass string
	// ChecksumAlgorithm is the checksum algorithm of the upload.
	ChecksumAlgorithm string
	// Owner is the account that owns the upload, or nil to omit it.
	Owner *Owner
	// PartNumberMarker is the part number the listing started after.
	PartNumberMarker int
	// NextPartNumberMarker is the marker for the next page.
	NextPartNumberMarker int
	// MaxParts is the maximum number of parts requested.
	MaxParts int
	// IsTruncated reports whether more parts are available.
	IsTruncated bool
	// Parts lists the uploaded parts.
	Parts []Part
}

// Upload describes one in-progress multipart upload. Owner and Initiator are
// both emitted with the same value only when Owner is non-nil.
type Upload struct {
	// Key is the object key being uploaded.
	Key string
	// UploadID identifies the multipart upload.
	UploadID string
	// StorageClass is the storage class of the upload.
	StorageClass string
	// Initiated is the time the upload was started.
	Initiated time.Time
	// Owner is the account that initiated the upload, or nil to omit it.
	Owner *Owner
}

// ListMultipartUploadsResult is a ListMultipartUploads response.
//
// Key-like values are percent-encoded when EncodingType is "url"; upload IDs
// are always escaped as text.
type ListMultipartUploadsResult struct {
	// Bucket is the bucket that holds the uploads.
	Bucket string
	// KeyMarker is the key marker supplied by the request.
	KeyMarker string
	// UploadIDMarker is the upload ID marker supplied by the request.
	UploadIDMarker string
	// NextKeyMarker is the key marker for the next page.
	NextKeyMarker string
	// NextUploadIDMarker is the upload ID marker for the next page.
	NextUploadIDMarker string
	// Prefix is the listing prefix.
	Prefix string
	// Delimiter is the grouping delimiter, if any.
	Delimiter string
	// EncodingType is "url" when key-like values are percent-encoded.
	EncodingType string
	// MaxUploads is the maximum number of uploads requested.
	MaxUploads int
	// IsTruncated reports whether more uploads are available.
	IsTruncated bool
	// Uploads lists the in-progress uploads.
	Uploads []Upload
	// CommonPrefixes lists the rolled-up common prefixes.
	CommonPrefixes []string
}

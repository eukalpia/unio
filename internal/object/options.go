package object

import "io"

// PutOptions carries the attributes of a new object version.
type PutOptions struct {
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

// GetOptions configures reads.
type GetOptions struct {
	VersionID  string
	Range      *HTTPRange
	PartNumber int
	Conditions Conditions
}

// Reader streams object content. Callers must Close it.
type Reader struct {
	io.ReadCloser
	Info Info
	// Offset and Length describe the window being served; for a full read
	// Offset is 0 and Length is Info.Size.
	Offset int64
	Length int64
}

// DeleteOptions configures deletion.
type DeleteOptions struct {
	VersionID  string
	Conditions Conditions
}

// ToDelete names one object in a DeleteObjects call.
type ToDelete struct {
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

// MetadataDirective controls metadata handling in CopyObject.
type MetadataDirective uint8

// Metadata directives.
const (
	MetadataCopy MetadataDirective = iota
	MetadataReplace
)

// CopyOptions configures CopyObject and UploadPartCopy.
type CopyOptions struct {
	// SourceConditions are the x-amz-copy-source-if-* preconditions.
	SourceConditions Conditions
	// SourceRange is used by UploadPartCopy.
	SourceRange *HTTPRange
	Directive   MetadataDirective
	// Put holds the destination attributes when Directive is
	// MetadataReplace, and destination conditions in all cases.
	Put PutOptions
}

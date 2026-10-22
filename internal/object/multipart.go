package object

// PartInfo describes an uploaded multipart part.
type PartInfo struct {
	PartNumber int
	Size       int64
	ETag       string
	ModTime    int64 // unix nanoseconds
	Checksum   Checksum
}

// CompletePart is one entry of a CompleteMultipartUpload request. ETag is
// unquoted, like every ETag in this package.
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

// Upload describes an in-progress multipart upload.
type Upload struct {
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
	Uploads            []Upload
	CommonPrefixes     []string
	IsTruncated        bool
	NextKeyMarker      string
	NextUploadIDMarker string
}

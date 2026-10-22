package router

// Op identifies one S3 operation. The zero value is OpUnknown and is never
// returned by Match without an error.
type Op uint8

// S3 operations supported by the v0.1 front end.
const (
	OpUnknown Op = iota
	OpListBuckets
	OpCreateBucket
	OpHeadBucket
	OpDeleteBucket
	OpGetBucketLocation
	OpListObjectsV1
	OpListObjectsV2
	OpListMultipartUploads
	OpDeleteObjects
	OpPutObject
	OpCopyObject
	OpGetObject
	OpHeadObject
	OpDeleteObject
	OpCreateMultipartUpload
	OpUploadPart
	OpUploadPartCopy
	OpCompleteMultipartUpload
	OpAbortMultipartUpload
	OpListParts
)

var opNames = [...]string{
	OpUnknown:                 "Unknown",
	OpListBuckets:             "ListBuckets",
	OpCreateBucket:            "CreateBucket",
	OpHeadBucket:              "HeadBucket",
	OpDeleteBucket:            "DeleteBucket",
	OpGetBucketLocation:       "GetBucketLocation",
	OpListObjectsV1:           "ListObjects",
	OpListObjectsV2:           "ListObjectsV2",
	OpListMultipartUploads:    "ListMultipartUploads",
	OpDeleteObjects:           "DeleteObjects",
	OpPutObject:               "PutObject",
	OpCopyObject:              "CopyObject",
	OpGetObject:               "GetObject",
	OpHeadObject:              "HeadObject",
	OpDeleteObject:            "DeleteObject",
	OpCreateMultipartUpload:   "CreateMultipartUpload",
	OpUploadPart:              "UploadPart",
	OpUploadPartCopy:          "UploadPartCopy",
	OpCompleteMultipartUpload: "CompleteMultipartUpload",
	OpAbortMultipartUpload:    "AbortMultipartUpload",
	OpListParts:               "ListParts",
}

// String returns the S3 operation name, for logs and metrics.
func (o Op) String() string {
	if int(o) < len(opNames) {
		return opNames[o]
	}
	return opNames[OpUnknown]
}

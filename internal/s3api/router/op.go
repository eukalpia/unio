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

	// Service endpoints of the reserved /unio/ namespace. They are not S3
	// operations and never name a bucket.
	OpHealthLive
	OpHealthReady
	OpMetrics
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
	OpHealthLive:              "HealthLive",
	OpHealthReady:             "HealthReady",
	OpMetrics:                 "Metrics",
}

// String returns the S3 operation name, for logs and metrics.
func (o Op) String() string {
	if int(o) < len(opNames) {
		return opNames[o]
	}
	return opNames[OpUnknown]
}

// IsService reports whether o is a service endpoint of the reserved
// namespace rather than an S3 operation.
func (o Op) IsService() bool {
	return o == OpHealthLive || o == OpHealthReady || o == OpMetrics
}

package s3api

import (
	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api/router"
)

// primaryAction maps a routed operation to the IAM action checked before the
// operation runs. Operations that touch a second object, such as CopyObject's
// source or DeleteObjects' keys, are authorized again by their handlers.
func primaryAction(op router.Op) auth.Action {
	switch op {
	case router.OpListBuckets:
		return auth.ActionListAllMyBuckets
	case router.OpCreateBucket:
		return auth.ActionCreateBucket
	case router.OpHeadBucket, router.OpListObjectsV1, router.OpListObjectsV2:
		return auth.ActionListBucket
	case router.OpDeleteBucket:
		return auth.ActionDeleteBucket
	case router.OpGetBucketLocation:
		return auth.ActionGetBucketLocation
	case router.OpListMultipartUploads:
		return auth.ActionListBucketMultipartUploads
	case router.OpDeleteObjects, router.OpDeleteObject:
		return auth.ActionDeleteObject
	case router.OpGetObject, router.OpHeadObject:
		return auth.ActionGetObject
	case router.OpAbortMultipartUpload:
		return auth.ActionAbortMultipartUpload
	case router.OpListParts:
		return auth.ActionListMultipartUploadParts
	default:
		return auth.ActionPutObject
	}
}

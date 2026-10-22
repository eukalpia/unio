package s3api

import (
	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api/router"
)

// requiresPreAuthorization reports whether the dispatcher checks primaryAction
// before the handler runs. DeleteObjects names its keys in the body, so its
// handler authorizes each key and reports denials per key. A request-level check
// against an empty key would refuse a caller that may delete some of the keys.
func requiresPreAuthorization(op router.Op) bool {
	return op != router.OpDeleteObjects
}

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
	case router.OpGetObjectTagging:
		return auth.ActionGetObjectTagging
	case router.OpPutObjectTagging:
		return auth.ActionPutObjectTagging
	case router.OpDeleteObjectTagging:
		return auth.ActionDeleteObjectTagging
	default:
		return auth.ActionPutObject
	}
}

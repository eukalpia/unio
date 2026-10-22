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
// source or DeleteObjects' keys, are authorized again by their handlers. It
// reports false for an operation with no mapping, which is then refused: a
// new operation added without an action must be denied, never authorized as
// some other action.
func primaryAction(op router.Op) (auth.Action, bool) {
	switch op {
	case router.OpListBuckets:
		return auth.ActionListAllMyBuckets, true
	case router.OpCreateBucket:
		return auth.ActionCreateBucket, true
	case router.OpHeadBucket, router.OpListObjectsV1, router.OpListObjectsV2:
		return auth.ActionListBucket, true
	case router.OpListObjectVersions:
		return auth.ActionListBucketVersions, true
	case router.OpDeleteBucket:
		return auth.ActionDeleteBucket, true
	case router.OpGetBucketLocation:
		return auth.ActionGetBucketLocation, true
	case router.OpListMultipartUploads:
		return auth.ActionListBucketMultipartUploads, true
	case router.OpDeleteObjects, router.OpDeleteObject:
		return auth.ActionDeleteObject, true
	case router.OpGetObject, router.OpHeadObject:
		return auth.ActionGetObject, true
	case router.OpPutObject, router.OpCopyObject, router.OpCreateMultipartUpload,
		router.OpUploadPart, router.OpUploadPartCopy, router.OpCompleteMultipartUpload:
		return auth.ActionPutObject, true
	case router.OpAbortMultipartUpload:
		return auth.ActionAbortMultipartUpload, true
	case router.OpListParts:
		return auth.ActionListMultipartUploadParts, true
	case router.OpGetObjectTagging:
		return auth.ActionGetObjectTagging, true
	case router.OpPutObjectTagging:
		return auth.ActionPutObjectTagging, true
	case router.OpDeleteObjectTagging:
		return auth.ActionDeleteObjectTagging, true
	default:
		return "", false
	}
}

package s3api

import (
	"testing"

	"github.com/eukalpia/unio/internal/s3api/router"
)

// TestEveryS3OperationHasAnAction checks that every S3 operation the router
// can produce maps to an IAM action. Operations are listed contiguously from
// ListBuckets to ListObjectVersions in router/op.go; a new operation added
// there without an action fails here instead of being refused at run time.
func TestEveryS3OperationHasAnAction(t *testing.T) {
	for op := router.OpListBuckets; op <= router.OpListObjectVersions; op++ {
		if _, ok := primaryAction(op); !ok {
			t.Errorf("operation %s has no IAM action", op)
		}
	}
}

// TestUnmappedOperationIsRefused checks that an operation without a mapping
// is refused rather than authorized as some other action.
func TestUnmappedOperationIsRefused(t *testing.T) {
	for _, op := range []router.Op{router.OpUnknown, router.OpHealthLive, router.OpAdminAPI, router.Op(255)} {
		if action, ok := primaryAction(op); ok {
			t.Errorf("operation %v maps to %q; it must be refused", op, action)
		}
	}
}

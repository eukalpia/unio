package fsm

import (
	"testing"

	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// TestSetControlRejectsUnknownVersioning checks that a versioning state outside
// the defined range is refused rather than truncated into the stored byte.
func TestSetControlRejectsUnknownVersioning(t *testing.T) {
	h := newHarness(t, KindData)
	b := h.f.db.NewBatch()
	t.Cleanup(func() { _ = b.Close() })
	for _, v := range []pb.VersioningState{-1, 3, 256, 1 << 20} {
		if err := setControl(b, testBucket, bucketControl{versioning: v}); err == nil {
			t.Fatalf("setControl accepted versioning state %d", v)
		}
	}
	if err := setControl(b, testBucket, bucketControl{versioning: pb.VersioningState_VERSIONING_STATE_SUSPENDED}); err != nil {
		t.Fatalf("setControl rejected a known state: %v", err)
	}
}

package erasuretest

import (
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

// TestFakeLayoutHasDistinctDriveIDs: every shard of a fake layout is recorded
// on its own non-zero drive, so a layout is never mistaken for an unwritten
// shard.
func TestFakeLayoutHasDistinctDriveIDs(t *testing.T) {
	s := NewFakeSet(7, 4, 2)
	layout := s.layout()
	seen := map[ids.DriveID]bool{}
	for i, d := range layout.Drives {
		if d.IsZero() {
			t.Fatalf("drive %d is the zero id", i)
		}
		if seen[d] {
			t.Fatalf("drive %d repeats an earlier id", i)
		}
		seen[d] = true
	}
	if layout.Written() != 6 {
		t.Fatalf("Written = %d, want 6", layout.Written())
	}
}

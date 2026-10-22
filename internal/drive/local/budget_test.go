package local

import (
	"context"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

// TestBudgetTooSmallForAPoolIsRejected checks that a buffer budget too small
// for one blob buffer is an Open error. The blob pool's charge includes its
// alignment slack, so a budget of the blob chunk plus less than the slack
// used to pass the check and then panic when the pool was created.
func TestBudgetTooSmallForAPoolIsRejected(t *testing.T) {
	tests := []struct {
		name    string
		budget  int64
		wantErr bool
	}{
		{"budget equal to the blob chunk", blobChunk, true},
		{"budget one byte short of the blob charge", blobChunk + blobAlign - 1, true},
		{"budget holds the blob charge", blobChunk + blobAlign, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ctx := context.Background()
			if err := FormatWithID(ctx, dir, "c", ids.NewDriveID()); err != nil {
				t.Fatalf("format: %v", err)
			}
			d, err := Open(ctx, dir, Options{
				ClusterID:    "c",
				BufferBudget: tc.budget,
				SegmentSize:  4 << 20,
				CompactRate:  -1,
			})
			if tc.wantErr {
				if err == nil {
					_ = d.Close()
					t.Fatal("open accepted a budget too small for a pool")
				}
				return
			}
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if err := d.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
		})
	}
}

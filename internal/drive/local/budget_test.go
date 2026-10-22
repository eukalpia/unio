package local

import (
	"context"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/ids"
)

// openOnBudget formats a fresh directory and opens a drive on the given budget.
func openOnBudget(t *testing.T, budget *bufpool.Budget) *Drive {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	if err := FormatWithID(ctx, dir, "c", ids.NewDriveID()); err != nil {
		t.Fatalf("format: %v", err)
	}
	d, err := Open(ctx, dir, Options{ClusterID: "c", Budget: budget, SegmentSize: 4 << 20, CompactRate: -1})
	if err != nil {
		t.Fatalf("open on shared budget: %v", err)
	}
	return d
}

// heldShardSize is small enough to take the small buffer class.
const heldShardSize = 100

// startHeldWrite starts a packed write on d and feeds it half of its payload.
// A drive takes its buffer before it reads the payload, so when the first half
// has been read the buffer is held. The returned function sends the rest and
// returns the result of the write.
func startHeldWrite(t *testing.T, d *Drive, n byte) func() error {
	t.Helper()
	var id ids.ShardID
	id.Data[0] = n
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- d.WriteShard(context.Background(), id, pr, heldShardSize) }()
	if _, err := pw.Write(make([]byte, heldShardSize/2)); err != nil {
		t.Fatalf("feed first half of shard %d: %v", n, err)
	}
	return func() error {
		if _, err := pw.Write(make([]byte, heldShardSize/2)); err != nil {
			return err
		}
		if err := pw.Close(); err != nil {
			return err
		}
		return <-done
	}
}

// TestSharedBudgetDrivesDrawFromOneBudget holds a buffer in each of two
// drives on one budget and checks that both are charged to it. Closing the
// drives returns every byte handed out.
func TestSharedBudgetDrivesDrawFromOneBudget(t *testing.T) {
	const total = 16 << 20
	budget := bufpool.NewBudget(total)
	a := openOnBudget(t, budget)
	b := openOnBudget(t, budget)

	r, err := Options{Budget: budget}.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	charge := r.smallPoolSize()

	if got := budget.Total(); got != total {
		t.Fatalf("Total = %d, want %d", got, total)
	}
	if got := budget.InUse(); got != 0 {
		t.Fatalf("InUse before any write = %d, want 0", got)
	}

	finishA := startHeldWrite(t, a, 1)
	if got := budget.InUse(); got != charge {
		t.Fatalf("InUse with one held write on drive a = %d, want %d", got, charge)
	}
	finishB := startHeldWrite(t, b, 2)
	if got := budget.InUse(); got != 2*charge {
		t.Fatalf("InUse with held writes on both drives = %d, want %d", got, 2*charge)
	}
	if err := finishA(); err != nil {
		t.Fatalf("finish write on a: %v", err)
	}
	if err := finishB(); err != nil {
		t.Fatalf("finish write on b: %v", err)
	}
	if got := budget.InUse(); got != 0 {
		t.Fatalf("InUse after the writes = %d, want 0", got)
	}
	if got := budget.Total(); got != total {
		t.Fatalf("Total changed to %d, want %d", got, total)
	}

	if err := a.Close(); err != nil {
		t.Fatalf("close a: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close b: %v", err)
	}
	if got := budget.InUse(); got != 0 {
		t.Fatalf("InUse after closing both drives = %d, want 0", got)
	}
}

// TestSharedBudgetReplacesBufferBudget checks that a shared budget overrides
// BufferBudget rather than being checked against it.
func TestSharedBudgetReplacesBufferBudget(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if err := FormatWithID(ctx, dir, "c", ids.NewDriveID()); err != nil {
		t.Fatalf("format: %v", err)
	}
	d, err := Open(ctx, dir, Options{
		ClusterID:    "c",
		Budget:       bufpool.NewBudget(8 << 20),
		BufferBudget: 1,
		SegmentSize:  4 << 20,
		CompactRate:  -1,
	})
	if err != nil {
		t.Fatalf("open with a shared budget and a tiny BufferBudget: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestBudgetTooSmallForAPoolIsRejected checks that a budget that cannot hold
// one buffer of a pool is an Open error. The blob pool's charge includes its
// alignment slack, so a budget equal to the blob chunk is too small.
func TestBudgetTooSmallForAPoolIsRejected(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{"private budget equal to the blob chunk", Options{BufferBudget: blobChunk}, true},
		{"shared budget equal to the blob chunk", Options{Budget: bufpool.NewBudget(blobChunk)}, true},
		{"shared budget one byte short of the blob charge", Options{Budget: bufpool.NewBudget(blobChunk + blobAlign - 1)}, true},
		{"shared budget holds the blob charge", Options{Budget: bufpool.NewBudget(blobChunk + blobAlign)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ctx := context.Background()
			if err := FormatWithID(ctx, dir, "c", ids.NewDriveID()); err != nil {
				t.Fatalf("format: %v", err)
			}
			opts := tc.opts
			opts.ClusterID = "c"
			opts.SegmentSize = 4 << 20
			opts.CompactRate = -1
			d, err := Open(ctx, dir, opts)
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

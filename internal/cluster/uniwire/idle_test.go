package uniwire

import (
	"context"
	"testing"

	"github.com/eukalpia/unio/internal/bufpool"
)

// TestCopyPoolKeepsIdleBuffersForReuse releases more copy buffers
// than a pool keeps by default and checks that every one is kept for reuse,
// so that the next transfers take them without allocating.
func TestCopyPoolKeepsIdleBuffersForReuse(t *testing.T) {
	const size = 256 << 10
	const buffers = 40
	budget := bufpool.NewBudget(buffers * size)
	tr, err := New(Config{Limits: Limits{
		StreamBufferSize: size,
		StreamBudget:     budget,
		ServeBudget:      bufpool.NewBudget(size),
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = tr.Close() }()

	ctx := context.Background()
	held := make([]*bufpool.Buffer, 0, buffers)
	for range buffers {
		b, err := tr.clientBuffers.Get(ctx)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		held = append(held, b)
	}
	for _, b := range held {
		b.Release()
	}
	if got, want := budget.Idle(), int64(buffers*size); got != want {
		t.Fatalf("Idle after releasing %d buffers = %d, want %d", buffers, got, want)
	}
}

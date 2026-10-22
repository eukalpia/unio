package rs

import (
	"context"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

func TestHealQueueDropsWhenFull(t *testing.T) {
	q := newHealQueue(2)
	req := HealRequest{Data: ids.DataID{1}, Layout: erasure.Layout{DataShards: 1}, Size: 9, Shard: 0}
	for i := range 2 {
		req.Shard = i
		if !q.Push(req) {
			t.Fatalf("Push %d rejected with room left", i)
		}
	}
	req.Shard = 2
	if q.Push(req) {
		t.Fatal("Push accepted a request into a full queue")
	}
	if got := q.Dropped(); got != 1 {
		t.Fatalf("Dropped = %d, want 1", got)
	}
	if got := q.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
}

func TestHealQueuePopIsFIFOAndHonoursContext(t *testing.T) {
	q := newHealQueue(4)
	for i := range 3 {
		q.Push(HealRequest{Shard: i})
	}
	for i := range 3 {
		got, err := q.Pop(context.Background())
		if err != nil || got.Shard != i {
			t.Fatalf("Pop = (%d, %v), want shard %d", got.Shard, err, i)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.Pop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Pop on empty queue with cancelled context: err = %v", err)
	}
}

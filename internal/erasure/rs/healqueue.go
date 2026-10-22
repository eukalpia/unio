package rs

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// HealRequest reports one shard that a read found damaged or missing. Heal
// the object with the given layout and size to repair it.
type HealRequest struct {
	// Data is the object the shard belongs to.
	Data ids.DataID
	// Layout is the layout the read used.
	Layout erasure.Layout
	// Size is the object size.
	Size int64
	// Shard is the index of the damaged shard.
	Shard int
}

// HealQueue is a bounded queue of heal requests. Push never blocks: when the
// queue is full the request is dropped and counted, because a read must not
// wait for repair work.
type HealQueue struct {
	ch      chan HealRequest
	dropped atomic.Uint64
}

// newHealQueue returns an empty queue that holds up to capacity requests.
func newHealQueue(capacity int) *HealQueue {
	return &HealQueue{ch: make(chan HealRequest, capacity)}
}

// Push queues req and reports whether it was accepted. A full queue drops
// req and increments Dropped.
func (q *HealQueue) Push(req HealRequest) bool {
	select {
	case q.ch <- req:
		return true
	default:
		q.dropped.Add(1)
		return false
	}
}

// Pop waits for the next request. It returns the context error if ctx is done
// first.
func (q *HealQueue) Pop(ctx context.Context) (HealRequest, error) {
	select {
	case req := <-q.ch:
		return req, nil
	case <-ctx.Done():
		return HealRequest{}, fmt.Errorf("erasure/rs: heal queue: %w", ctx.Err())
	}
}

// Len returns the number of queued requests.
func (q *HealQueue) Len() int {
	return len(q.ch)
}

// Dropped returns the number of requests dropped because the queue was full.
func (q *HealQueue) Dropped() uint64 {
	return q.dropped.Load()
}

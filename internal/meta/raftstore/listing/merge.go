package listing

import (
	"container/heap"
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/eukalpia/unio/internal/ids"
)

// maxFanout bounds the first fetches in flight. The partition count comes
// from configuration. The bound keeps one listing from opening a request per
// partition at once in a large cluster.
const maxFanout = 16

// maxBatch caps the items requested from one partition per fetch. A page
// needs at most MaxKeys+1 items from any one partition, the extra one being
// the lookahead. A fetch therefore asks for min(MaxKeys+1, maxBatch) items,
// both for a partition's first batch and for every refill.
const maxBatch = 256

// batchLimit returns the item limit of every fetch of a page of maxKeys entries.
func batchLimit(maxKeys int) int {
	return min(maxKeys+1, maxBatch)
}

// partStream is one partition's stream: the batch being consumed and what is
// needed to refill it.
type partStream struct {
	part      ids.PartitionID
	items     []Item
	idx       int  // position of the head in items
	exhausted bool // the partition has no item after items
	// prev is the Sort of the last item consumed from the partition. Items
	// must strictly increase across batches.
	prev    string
	hasPrev bool
}

// streamHeap orders partition streams by the Sort of their head item.
type streamHeap []*partStream

func (h streamHeap) Len() int { return len(h) }

func (h streamHeap) Less(i, j int) bool {
	return h[i].items[h[i].idx].Sort < h[j].items[h[j].idx].Sort
}

func (h streamHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *streamHeap) Push(x any) { *h = append(*h, x.(*partStream)) }

func (h *streamHeap) Pop() any {
	old := *h
	n := len(old)
	s := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return s
}

// merger yields the entries of all partitions in listing order, each common
// prefix once.
type merger struct {
	ctx   context.Context
	fetch Fetch
	limit int
	// after is the start cursor. Entries at or before it are dropped.
	after   string
	streams streamHeap
	// pending is a partition whose buffer is consumed. It is refetched only
	// when the next entry is needed, so no partition is read past the page.
	pending *partStream
	// lastCP is the last common prefix returned. Other partitions may report
	// the same prefix, and it is dropped the second time.
	lastCP string
	haveCP bool
}

// newMerger fetches the first batch of every partition, at most maxFanout at
// a time, and returns the merger over them. It returns only after every fetch
// has finished, so no goroutine outlives the call.
func newMerger(ctx context.Context, parts []ids.PartitionID, fetch Fetch, limit int, after string) (*merger, error) {
	batches := make([]Batch, len(parts))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxFanout)
	for i, p := range parts {
		if gctx.Err() != nil {
			break // a failure or cancellation stops new fetches
		}
		g.Go(func() error {
			b, err := fetch(gctx, p, after, limit)
			if err != nil {
				return fmt.Errorf("partition %d: %w", p, err)
			}
			if err := checkBatch(p, b); err != nil {
				return err
			}
			batches[i] = b
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := &merger{ctx: ctx, fetch: fetch, limit: limit, after: after}
	for i, p := range parts {
		if len(batches[i].Items) > 0 {
			m.streams = append(m.streams, &partStream{part: p, items: batches[i].Items, exhausted: batches[i].Exhausted})
		}
	}
	heap.Init(&m.streams)
	return m, nil
}

// checkBatch rejects an empty batch that claims the partition has more items,
// which would stall the merge.
func checkBatch(p ids.PartitionID, b Batch) error {
	if len(b.Items) == 0 && !b.Exhausted {
		return fmt.Errorf("partition %d: empty batch without exhaustion", p)
	}
	return nil
}

// refill fetches the batch that follows the last item of s's consumed buffer.
func (m *merger) refill(s *partStream) error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	last := s.items[len(s.items)-1].Sort
	b, err := m.fetch(m.ctx, s.part, last, m.limit)
	if err != nil {
		return fmt.Errorf("partition %d: %w", s.part, err)
	}
	if err := checkBatch(s.part, b); err != nil {
		return err
	}
	s.items, s.idx, s.exhausted = b.Items, 0, b.Exhausted
	return nil
}

// next returns the next entry of the merged listing that the page may emit,
// or nil when the listing is exhausted. The entry points into a batch and
// stays valid after the call.
func (m *merger) next() (*Item, error) {
	for {
		if s := m.pending; s != nil {
			m.pending = nil
			if err := m.refill(s); err != nil {
				return nil, err
			}
			if len(s.items) > 0 {
				heap.Push(&m.streams, s)
			}
		}
		if len(m.streams) == 0 {
			return nil, nil
		}
		s := m.streams[0]
		it := &s.items[s.idx]
		if s.hasPrev && it.Sort <= s.prev {
			return nil, fmt.Errorf("partition %d: items out of order at %q", s.part, it.Sort)
		}
		s.prev, s.hasPrev = it.Sort, true
		s.idx++
		if s.idx < len(s.items) {
			heap.Fix(&m.streams, 0)
		} else {
			heap.Pop(&m.streams)
			if !s.exhausted {
				m.pending = s
			}
		}
		if m.skip(it) {
			continue
		}
		return it, nil
	}
}

// skip reports whether the page must drop it. An entry at or before the start
// cursor is dropped as a guard: partitions honour the cursor, so this path is
// not expected to run. A common prefix already returned by another partition
// is dropped as well.
func (m *merger) skip(it *Item) bool {
	if m.after != "" && it.Sort <= m.after {
		return true
	}
	if !it.CommonPrefix {
		return false
	}
	if m.haveCP && it.Sort == m.lastCP {
		return true
	}
	m.lastCP, m.haveCP = it.Sort, true
	return false
}

// page emits up to n entries through emit. It reports whether another entry
// follows them, which costs one lookahead entry that is read and not emitted.
// last is the last entry emitted, or nil for an empty page.
func (m *merger) page(n int, emit func(*Item)) (bool, *Item, error) {
	var last *Item
	for range n {
		it, err := m.next()
		if err != nil {
			return false, nil, err
		}
		if it == nil {
			return false, last, nil
		}
		emit(it)
		last = it
	}
	it, err := m.next()
	if err != nil {
		return false, nil, err
	}
	return it != nil, last, nil
}

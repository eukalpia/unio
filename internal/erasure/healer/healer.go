// Package healer repairs damaged shards in the background. It consumes the heal
// queues of a set of erasure sets, which reads fill when they find a damaged or
// missing shard, and heals each reported object with a bounded pool of workers.
package healer

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/rs"
	"github.com/eukalpia/unio/internal/ids"
)

// Healer repairs the objects that the heal queues of its sets report. It is safe
// for concurrent use. Run must be called at most once.
type Healer struct {
	sets    []*rs.Set
	workers int
	timeout time.Duration
	logger  *slog.Logger

	// jobs hands accepted requests from the feeders to the workers. Its
	// capacity equals the number of workers, so each worker has at most one
	// request waiting for it.
	jobs chan job

	mu sync.Mutex
	// active holds the objects with a request accepted and not yet finished.
	active map[flightKey]struct{}

	// held counts requests popped from a queue and not yet finished.
	held   atomic.Int64
	healed atomic.Uint64
	failed atomic.Uint64
}

// flightKey identifies one object within the sets of a healer.
type flightKey struct {
	set  ids.SetID
	data ids.DataID
}

// job is one accepted request, with the set that must heal it.
type job struct {
	set *rs.Set
	req rs.HealRequest
	key flightKey
}

// New returns a healer for sets. Every set must be non-nil. Workers are shared
// by all sets, so one busy set cannot take all of them.
func New(sets []*rs.Set, opts Options) *Healer {
	if opts.Workers <= 0 {
		opts.Workers = DefaultWorkers
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Healer{
		sets:    slices.Clone(sets),
		workers: opts.Workers,
		timeout: opts.Timeout,
		logger:  opts.Logger,
		jobs:    make(chan job, opts.Workers),
		active:  make(map[flightKey]struct{}),
	}
}

// Run heals the objects that the sets report until ctx is done. It starts one
// feeder per set, which pops that set's heal queue, and Workers workers that
// drain the shared hand-off channel. The sets take turns at the channel, so a
// busy set cannot starve the others. Run returns only after every goroutine it
// started has exited.
func (h *Healer) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, s := range h.sets {
		wg.Go(func() { h.feed(ctx, s) })
	}
	for range h.workers {
		wg.Go(func() { h.work(ctx) })
	}
	wg.Wait()
	// The feeders and workers have exited, so nothing else sends on jobs. Finish
	// the requests that were handed off but never taken, so that Stats does not
	// report them as pending forever.
	for {
		select {
		case j := <-h.jobs:
			h.finish(j.key)
		default:
			return
		}
	}
}

// feed pops the damage reports of one set and hands each object to a worker.
// A report for an object that already has a request accepted is skipped, since
// one heal of the object repairs all its damaged shards. The feeder returns when
// ctx is done.
func (h *Healer) feed(ctx context.Context, s *rs.Set) {
	q := s.HealQueue()
	for {
		req, err := q.Pop(ctx)
		if err != nil {
			return
		}
		key := flightKey{set: s.ID(), data: req.Data}
		if !h.claim(key) {
			h.logger.Debug("heal request already in flight", "set", key.set, "data", key.data)
			continue
		}
		h.held.Add(1)
		select {
		case h.jobs <- job{set: s, req: req, key: key}:
		case <-ctx.Done():
			h.finish(key)
			return
		}
	}
}

// work heals the objects that the feeders hand over until ctx is done.
func (h *Healer) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-h.jobs:
			h.heal(ctx, j)
		}
	}
}

// heal repairs one object under the healer's timeout and records the outcome.
// The timeout is derived from ctx, so shutdown cancels a heal in progress.
func (h *Healer) heal(ctx context.Context, j job) {
	defer h.finish(j.key)
	hctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	req := j.req
	layout, err := j.set.Heal(hctx, req.Data, req.Layout, req.Size)
	if err != nil {
		h.failed.Add(1)
		h.logger.Warn("heal failed", "set", j.key.set, "data", req.Data, "shard", req.Shard, "err", err)
		return
	}
	h.healed.Add(1)
	// Heal repairs shards in place, on the drives that the layout records, so
	// the layout comes back unchanged and the stored metadata stays valid. A
	// different layout means that shards moved to other drives. The healer
	// does not persist layouts, so it reports the change for the operator.
	if !sameLayout(layout, req.Layout) {
		h.logger.Warn("heal changed the object layout", "set", j.key.set, "data", req.Data)
	}
}

// claim marks key as having a request accepted. It reports false when the key
// already has one.
func (h *Healer) claim(key flightKey) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, busy := h.active[key]; busy {
		return false
	}
	h.active[key] = struct{}{}
	return true
}

// finish releases the claim on key and the request's share of Pending.
func (h *Healer) finish(key flightKey) {
	h.mu.Lock()
	delete(h.active, key)
	h.mu.Unlock()
	h.held.Add(-1)
}

// Stats returns the counters of the healer. It is safe for concurrent use.
func (h *Healer) Stats() Stats {
	st := Stats{
		Healed:  h.healed.Load(),
		Failed:  h.failed.Load(),
		Pending: int(h.held.Load()),
	}
	for _, s := range h.sets {
		q := s.HealQueue()
		st.Dropped += q.Dropped()
		st.Pending += q.Len()
	}
	return st
}

// sameLayout reports whether a and b record the same placement.
func sameLayout(a, b erasure.Layout) bool {
	return a.Set == b.Set &&
		a.DataShards == b.DataShards &&
		a.ParityShards == b.ParityShards &&
		a.StripeSize == b.StripeSize &&
		slices.Equal(a.Drives, b.Drives)
}

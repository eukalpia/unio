// Package gc deletes the erasure-coded data of object versions that are no
// longer referenced by metadata.
//
// The queue is deliberately simple: a bounded in-memory backlog drained by a
// fixed pool of workers. Metadata commits never wait for deletions, and the
// backlog never grows past its configured size. Deletions that cannot be
// queued (backlog full, or the queue is closed) and deletions that fail are
// logged and dropped. Their shards become orphans, which the background
// scrubber finds and reclaims. This trades a delay in reclaiming space for
// bounded memory and for never blocking a request on cleanup.
package gc

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// DefaultTimeout bounds one deletion when Config.Timeout is zero.
const DefaultTimeout = 30 * time.Second

// Task asks for the shards of Data, laid out as Layout, to be removed from
// Set.
type Task struct {
	Set    erasure.Set
	Data   ids.DataID
	Layout erasure.Layout
}

// Config configures a Queue.
type Config struct {
	// QueueSize bounds the backlog of accepted deletions. Must be positive.
	QueueSize int
	// Workers is the number of concurrent deletions. Must be positive.
	Workers int
	// Timeout bounds one deletion. Zero means DefaultTimeout.
	Timeout time.Duration
	// Logger receives drop and failure reports. Nil discards them.
	Logger *slog.Logger
}

// Queue runs deletions in the background. It is safe for concurrent use.
type Queue struct {
	logger  *slog.Logger
	timeout time.Duration
	tasks   chan Task
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup

	// mu orders Enqueue against Close: senders hold the read side, Close
	// takes the write side before closing the channel.
	mu     sync.RWMutex
	closed bool

	// pendingMu guards pending and idle. idle is closed when pending drops
	// to zero and recreated when it rises from zero.
	pendingMu sync.Mutex
	pending   int
	idle      chan struct{}

	completed atomic.Uint64
	failed    atomic.Uint64
	dropped   atomic.Uint64
}

// New starts a queue with cfg.Workers workers.
func New(cfg Config) (*Queue, error) {
	if cfg.QueueSize <= 0 {
		return nil, fmt.Errorf("gc: queue size must be positive, got %d", cfg.QueueSize)
	}
	if cfg.Workers <= 0 {
		return nil, fmt.Errorf("gc: workers must be positive, got %d", cfg.Workers)
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue{
		logger:  logger,
		timeout: timeout,
		tasks:   make(chan Task, cfg.QueueSize),
		ctx:     ctx,
		cancel:  cancel,
	}
	q.workers.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go q.work()
	}
	return q, nil
}

// Enqueue schedules t without blocking. It returns false, and logs the
// drop, when the backlog is full, the queue is closed, or t is incomplete.
// A false result leaves the shards to the scrubber.
func (q *Queue) Enqueue(t Task) bool {
	if t.Set == nil || t.Data.IsZero() {
		return false
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		q.drop(t, "queue closed")
		return false
	}
	q.addPending(1)
	select {
	case q.tasks <- t:
		return true
	default:
		q.addPending(-1)
		q.drop(t, "backlog full")
		return false
	}
}

// Wait blocks until every accepted deletion has finished or ctx is done.
// Tests and shutdown use it; requests never do.
func (q *Queue) Wait(ctx context.Context) error {
	q.pendingMu.Lock()
	if q.pending == 0 {
		q.pendingMu.Unlock()
		return nil
	}
	idle := q.idle
	q.pendingMu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("gc: waiting for deletions: %w", ctx.Err())
	}
}

// Close stops accepting deletions and waits for the accepted ones to finish.
// If ctx is done first, the remaining deletions are cancelled and Close
// returns ctx's error once the workers have exited. Close is idempotent.
func (q *Queue) Close(ctx context.Context) error {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.tasks)
	}
	q.mu.Unlock()

	done := make(chan struct{})
	go func() {
		q.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		q.cancel()
		return nil
	case <-ctx.Done():
		q.cancel()
		<-done
		return fmt.Errorf("gc: closing: %w", ctx.Err())
	}
}

// Pending returns the number of accepted deletions that have not finished.
func (q *Queue) Pending() int {
	q.pendingMu.Lock()
	defer q.pendingMu.Unlock()
	return q.pending
}

// Completed returns the number of deletions that succeeded.
func (q *Queue) Completed() uint64 { return q.completed.Load() }

// Failed returns the number of deletions that returned an error.
func (q *Queue) Failed() uint64 { return q.failed.Load() }

// Dropped returns the number of tasks that were not accepted.
func (q *Queue) Dropped() uint64 { return q.dropped.Load() }

func (q *Queue) work() {
	defer q.workers.Done()
	for t := range q.tasks {
		q.run(t)
		q.addPending(-1)
	}
}

func (q *Queue) run(t Task) {
	ctx, cancel := context.WithTimeout(q.ctx, q.timeout)
	defer cancel()
	if err := t.Set.Delete(ctx, t.Data, t.Layout); err != nil {
		q.failed.Add(1)
		q.logger.Warn("gc: deletion failed; the scrubber reclaims the shards",
			"data", t.Data.String(), "err", err)
		return
	}
	q.completed.Add(1)
}

func (q *Queue) drop(t Task, reason string) {
	q.dropped.Add(1)
	q.logger.Warn("gc: deletion dropped; the scrubber reclaims the shards",
		"data", t.Data.String(), "reason", reason)
}

func (q *Queue) addPending(delta int) {
	q.pendingMu.Lock()
	defer q.pendingMu.Unlock()
	if delta > 0 && q.pending == 0 {
		q.idle = make(chan struct{})
	}
	q.pending += delta
	if q.pending == 0 {
		close(q.idle)
	}
}

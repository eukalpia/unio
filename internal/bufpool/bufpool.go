// Package bufpool provides fixed-size, reusable buffers drawn from a shared
// memory budget. Every hot path allocates its I/O buffers here so that total
// buffer memory is bounded by configuration (architecture section 11).
//
// The budget charges the real size of every buffer that exists, whether it is
// in use or kept idle for reuse, including alignment slack. When the budget
// is exhausted, Get first reclaims idle buffers from every pool sharing the
// budget and then blocks until a buffer is released or the caller's context
// is done.
package bufpool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sync/semaphore"
)

// DefaultMaxIdle is the number of idle buffers a pool keeps for reuse unless
// WithMaxIdle says otherwise.
const DefaultMaxIdle = 16

// Budget is a memory limit shared by any number of pools.
type Budget struct {
	sem     *semaphore.Weighted
	total   int64
	inUse   atomic.Int64 // bytes charged for buffers handed out
	idle    atomic.Int64 // bytes charged for buffers kept for reuse
	waiters atomic.Int64 // Get calls blocked on the semaphore

	mu    sync.Mutex
	pools []*Pool
}

// NewBudget returns a budget of the given number of bytes.
func NewBudget(bytes int64) *Budget {
	if bytes <= 0 {
		panic("bufpool: budget must be positive") // programming error at startup
	}
	return &Budget{sem: semaphore.NewWeighted(bytes), total: bytes}
}

// Total returns the budget size in bytes.
func (b *Budget) Total() int64 { return b.total }

// InUse returns the bytes charged for buffers currently handed out.
func (b *Budget) InUse() int64 { return b.inUse.Load() }

// Idle returns the bytes charged for buffers kept for reuse.
func (b *Budget) Idle() int64 { return b.idle.Load() }

// reclaim drops the idle buffers of every pool sharing the budget, returning
// their bytes to the budget.
func (b *Budget) reclaim() {
	b.mu.Lock()
	pools := append([]*Pool(nil), b.pools...)
	b.mu.Unlock()
	for _, p := range pools {
		p.dropIdle()
	}
}

func (b *Budget) register(p *Pool) {
	b.mu.Lock()
	b.pools = append(b.pools, p)
	b.mu.Unlock()
}

// Pool hands out buffers of one size.
type Pool struct {
	size    int
	align   int
	charge  int64 // bytes actually allocated per buffer, alignment slack included
	maxIdle int
	budget  *Budget

	mu   sync.Mutex
	idle [][]byte
}

// Option configures a Pool.
type Option func(*Pool)

// WithAlignment makes every buffer start at an address that is a multiple
// of align (a power of two), as required for O_DIRECT I/O. The alignment
// slack is charged to the budget.
func WithAlignment(align int) Option {
	return func(p *Pool) { p.align = align }
}

// WithMaxIdle sets how many idle buffers the pool keeps for reuse. Zero
// disables reuse.
func WithMaxIdle(n int) Option {
	return func(p *Pool) { p.maxIdle = n }
}

// NewPool returns a pool of size-byte buffers charged against budget.
func NewPool(size int, budget *Budget, opts ...Option) *Pool {
	p := &Pool{size: size, align: 1, maxIdle: DefaultMaxIdle, budget: budget}
	for _, o := range opts {
		o(p)
	}
	if p.align <= 0 || p.align&(p.align-1) != 0 {
		panic(fmt.Sprintf("bufpool: alignment %d is not a power of two", p.align))
	}
	if p.maxIdle < 0 {
		panic(fmt.Sprintf("bufpool: negative idle limit %d", p.maxIdle))
	}
	p.charge = int64(size)
	if p.align > 1 {
		p.charge += int64(p.align)
	}
	if size <= 0 || p.charge > budget.total {
		panic(fmt.Sprintf("bufpool: buffer of %d bytes (%d charged) does not fit budget %d", size, p.charge, budget.total))
	}
	budget.register(p)
	return p
}

// Size returns the buffer size.
func (p *Pool) Size() int { return p.size }

// Buffer is a pooled buffer. Release it exactly once when done; further
// Releases are no-ops. The slice must not be used after Release.
type Buffer struct {
	pool     *Pool
	b        []byte
	released atomic.Bool
}

// Bytes returns the full-size buffer.
func (b *Buffer) Bytes() []byte { return b.b }

// Release returns the buffer to its pool. The buffer is kept for reuse, still
// charged to the budget, unless the pool is full or a Get is waiting for
// budget; otherwise its bytes return to the budget.
func (b *Buffer) Release() {
	if !b.released.CompareAndSwap(false, true) {
		return
	}
	p := b.pool
	bud := p.budget
	bud.inUse.Add(-p.charge)
	if bud.waiters.Load() == 0 && p.keepIdle(b.b) {
		// A Get may have started waiting after the check above and reclaimed
		// before the buffer was stored. Waiters register before reclaiming,
		// so re-checking here guarantees one side frees the idle budget.
		if bud.waiters.Load() > 0 {
			p.dropIdle()
		}
		return
	}
	bud.sem.Release(p.charge)
}

// Get returns a buffer, waiting for budget if necessary. It fails only when
// ctx is done.
func (p *Pool) Get(ctx context.Context) (*Buffer, error) {
	if buf, ok := p.TryGet(); ok {
		return buf, nil
	}
	bud := p.budget
	bud.waiters.Add(1)
	defer bud.waiters.Add(-1)
	bud.reclaim()
	if err := bud.sem.Acquire(ctx, p.charge); err != nil {
		return nil, fmt.Errorf("bufpool: waiting for %d bytes: %w", p.charge, err)
	}
	return p.handOut(p.alloc()), nil
}

// TryGet returns a buffer if one is idle or budget is available right now.
func (p *Pool) TryGet() (*Buffer, bool) {
	if b, ok := p.takeIdle(); ok {
		return p.handOut(b), true
	}
	if !p.budget.sem.TryAcquire(p.charge) {
		return nil, false
	}
	return p.handOut(p.alloc()), true
}

func (p *Pool) handOut(b []byte) *Buffer {
	p.budget.inUse.Add(p.charge)
	return &Buffer{pool: p, b: b}
}

// takeIdle pops an idle buffer; its budget charge moves from idle to in use.
func (p *Pool) takeIdle() ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.idle)
	if n == 0 {
		return nil, false
	}
	b := p.idle[n-1]
	p.idle[n-1] = nil
	p.idle = p.idle[:n-1]
	p.budget.idle.Add(-p.charge)
	return b, true
}

// keepIdle stores b for reuse, keeping its budget charge, if there is room.
func (p *Pool) keepIdle(b []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.idle) >= p.maxIdle {
		return false
	}
	p.idle = append(p.idle, b)
	p.budget.idle.Add(p.charge)
	return true
}

// dropIdle frees every idle buffer and returns their bytes to the budget.
func (p *Pool) dropIdle() {
	p.mu.Lock()
	n := len(p.idle)
	clear(p.idle)
	p.idle = p.idle[:0]
	p.mu.Unlock()
	if n == 0 {
		return
	}
	freed := int64(n) * p.charge
	p.budget.idle.Add(-freed)
	p.budget.sem.Release(freed)
}

func (p *Pool) alloc() []byte {
	if p.align == 1 {
		return make([]byte, p.size)
	}
	raw := make([]byte, p.size+p.align)
	off := 0
	// #nosec G103 -- the address is only read to compute an alignment offset;
	// no pointer arithmetic is converted back into a pointer.
	if rem := int(uintptr(unsafe.Pointer(&raw[0])) & uintptr(p.align-1)); rem != 0 {
		off = p.align - rem
	}
	return raw[off : off+p.size : off+p.size]
}

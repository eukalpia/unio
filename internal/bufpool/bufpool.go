// Package bufpool provides fixed-size, reusable buffers drawn from a shared
// memory budget. Every hot path allocates its I/O buffers here so that total
// buffer memory is bounded by configuration (architecture section 11):
// when the budget is exhausted, Get blocks until a buffer is released or the
// caller's context is done.
package bufpool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sync/semaphore"
)

// Budget is a memory limit shared by any number of pools.
type Budget struct {
	sem   *semaphore.Weighted
	total int64
	inUse atomic.Int64
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

// InUse returns the bytes currently held by outstanding buffers.
func (b *Budget) InUse() int64 { return b.inUse.Load() }

// Pool hands out buffers of one size.
type Pool struct {
	size   int
	align  int
	budget *Budget
	free   sync.Pool
}

// Option configures a Pool.
type Option func(*Pool)

// WithAlignment makes every buffer start at an address that is a multiple
// of align (a power of two), as required for O_DIRECT I/O.
func WithAlignment(align int) Option {
	return func(p *Pool) { p.align = align }
}

// NewPool returns a pool of size-byte buffers charged against budget.
func NewPool(size int, budget *Budget, opts ...Option) *Pool {
	if size <= 0 || int64(size) > budget.total {
		panic(fmt.Sprintf("bufpool: invalid buffer size %d for budget %d", size, budget.total))
	}
	p := &Pool{size: size, align: 1, budget: budget}
	for _, o := range opts {
		o(p)
	}
	if p.align <= 0 || p.align&(p.align-1) != 0 {
		panic(fmt.Sprintf("bufpool: alignment %d is not a power of two", p.align))
	}
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

// Release returns the buffer to its pool and frees its budget.
func (b *Buffer) Release() {
	if !b.released.CompareAndSwap(false, true) {
		return
	}
	p := b.pool
	p.free.Put(&b.b)
	p.budget.inUse.Add(-int64(p.size))
	p.budget.sem.Release(int64(p.size))
}

// Get returns a buffer, waiting for budget if necessary. It fails only when
// ctx is done.
func (p *Pool) Get(ctx context.Context) (*Buffer, error) {
	if err := p.budget.sem.Acquire(ctx, int64(p.size)); err != nil {
		return nil, fmt.Errorf("bufpool: waiting for %d bytes: %w", p.size, err)
	}
	return p.take(), nil
}

// TryGet returns a buffer if budget is available right now.
func (p *Pool) TryGet() (*Buffer, bool) {
	if !p.budget.sem.TryAcquire(int64(p.size)) {
		return nil, false
	}
	return p.take(), true
}

func (p *Pool) take() *Buffer {
	p.budget.inUse.Add(int64(p.size))
	if v, ok := p.free.Get().(*[]byte); ok {
		return &Buffer{pool: p, b: *v}
	}
	return &Buffer{pool: p, b: p.alloc()}
}

func (p *Pool) alloc() []byte {
	if p.align == 1 {
		return make([]byte, p.size)
	}
	raw := make([]byte, p.size+p.align)
	off := 0
	if rem := int(uintptr(unsafe.Pointer(&raw[0])) & uintptr(p.align-1)); rem != 0 {
		off = p.align - rem
	}
	return raw[off : off+p.size : off+p.size]
}

package bufpool

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// charged is everything the budget currently accounts for.
func charged(b *Budget) int64 { return b.InUse() + b.Idle() }

func TestIdleBuffersStayCharged(t *testing.T) {
	b := NewBudget(4 << 10)
	p := NewPool(1<<10, b)
	ctx := context.Background()
	var bufs []*Buffer
	for range 4 {
		buf, err := p.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		bufs = append(bufs, buf)
	}
	for _, buf := range bufs {
		buf.Release()
	}
	if b.InUse() != 0 || b.Idle() != 4<<10 {
		t.Fatalf("inUse %d idle %d, want 0 and %d", b.InUse(), b.Idle(), 4<<10)
	}
	// Reuse takes idle buffers without allocating new budget.
	for range 4 {
		if _, ok := p.TryGet(); !ok {
			t.Fatal("idle buffer not reused")
		}
	}
	if charged(b) != 4<<10 {
		t.Fatalf("charged %d", charged(b))
	}
}

// TestSharedBudgetNeverExceeded is the scenario from review: pools of
// different sizes share a budget, so idle buffers of one pool must not let
// another pool allocate beyond the limit.
func TestSharedBudgetNeverExceeded(t *testing.T) {
	const total = 8 << 10
	b := NewBudget(total)
	small := NewPool(1<<10, b)
	big := NewPool(4<<10, b)
	ctx := context.Background()

	// Fill the budget with small buffers, then release them all to idle.
	var bufs []*Buffer
	for range 8 {
		buf, err := small.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		bufs = append(bufs, buf)
	}
	for _, buf := range bufs {
		buf.Release()
	}
	if charged(b) > total {
		t.Fatalf("charged %d exceeds budget %d", charged(b), total)
	}
	// A big buffer must still be obtainable: idle small buffers are reclaimed.
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	g1, err := big.Get(ctx2)
	if err != nil {
		t.Fatalf("big Get blocked by idle buffers of another pool: %v", err)
	}
	g2, err := big.Get(ctx2)
	if err != nil {
		t.Fatalf("second big Get: %v", err)
	}
	if charged(b) > total {
		t.Fatalf("charged %d exceeds budget %d", charged(b), total)
	}
	g1.Release()
	g2.Release()
}

func TestAlignmentSlackIsCharged(t *testing.T) {
	// One aligned 4 KiB buffer really allocates 8 KiB.
	b := NewBudget(8 << 10)
	p := NewPool(4<<10, b, WithAlignment(4096))
	buf, ok := p.TryGet()
	if !ok {
		t.Fatal("first aligned buffer should fit")
	}
	if b.InUse() != 8<<10 {
		t.Fatalf("inUse %d, want alignment slack charged (%d)", b.InUse(), 8<<10)
	}
	if _, ok := p.TryGet(); ok {
		t.Fatal("second aligned buffer must not fit an 8 KiB budget")
	}
	buf.Release()
}

func TestAlignedPoolMustFitBudget(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for a pool whose charge exceeds the budget")
		}
	}()
	NewPool(4<<10, NewBudget(4<<10), WithAlignment(4096))
}

// TestWaiterIsNotStarvedByCaching checks that a released buffer goes to a
// waiting Get of another pool instead of an idle list.
func TestWaiterIsNotStarvedByCaching(t *testing.T) {
	b := NewBudget(2 << 10)
	a := NewPool(2<<10, b)
	c := NewPool(1<<10, b)
	held, err := a.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		buf, err := c.Get(ctx)
		if err == nil {
			buf.Release()
		}
		got <- err
	}()
	// Wait until the Get is registered as a waiter.
	for b.waiters.Load() == 0 {
		runtime.Gosched()
	}
	held.Release()
	if err := <-got; err != nil {
		t.Fatalf("waiter starved: %v", err)
	}
}

func TestMaxIdleZeroDisablesReuse(t *testing.T) {
	b := NewBudget(1 << 10)
	p := NewPool(1<<10, b, WithMaxIdle(0))
	buf, _ := p.TryGet()
	buf.Release()
	if b.Idle() != 0 || charged(b) != 0 {
		t.Fatalf("idle %d charged %d with reuse disabled", b.Idle(), charged(b))
	}
}

func TestChargedNeverExceedsTotalUnderLoad(t *testing.T) {
	const total = 64 << 10
	b := NewBudget(total)
	pools := []*Pool{NewPool(1<<10, b), NewPool(4<<10, b), NewPool(8<<10, b, WithAlignment(4096))}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var violation sync.Once
	var violated bool
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				if charged(b) > total {
					violation.Do(func() { violated = true })
				}
			}
		}
	}()
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := pools[i%len(pools)]
			for range 200 {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				buf, err := p.Get(ctx)
				cancel()
				if err != nil {
					t.Error(err)
					return
				}
				buf.Bytes()[0] = 1
				buf.Release()
			}
		}()
	}
	wg.Wait()
	close(stop)
	if violated {
		t.Fatal("charged bytes exceeded the budget")
	}
	if b.InUse() != 0 {
		t.Fatalf("inUse %d after all releases", b.InUse())
	}
}

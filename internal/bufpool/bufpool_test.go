package bufpool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
	"unsafe"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestGetRelease(t *testing.T) {
	b := NewBudget(4 << 10)
	p := NewPool(1<<10, b)
	buf, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(buf.Bytes()) != 1<<10 || b.InUse() != 1<<10 {
		t.Fatalf("len %d inUse %d", len(buf.Bytes()), b.InUse())
	}
	buf.Release()
	buf.Release() // idempotent
	if b.InUse() != 0 {
		t.Fatalf("inUse after release = %d", b.InUse())
	}
}

func TestBudgetBlocksUntilRelease(t *testing.T) {
	b := NewBudget(2 << 10)
	p := NewPool(1<<10, b)
	ctx := context.Background()
	b1, _ := p.Get(ctx)
	b2, _ := p.Get(ctx)
	if _, ok := p.TryGet(); ok {
		t.Fatal("TryGet should fail when budget is exhausted")
	}
	got := make(chan *Buffer)
	go func() {
		b3, err := p.Get(ctx)
		if err != nil {
			t.Error(err)
		}
		got <- b3
	}()
	select {
	case <-got:
		t.Fatal("Get returned before budget was released")
	case <-time.After(20 * time.Millisecond):
	}
	b1.Release()
	b3 := <-got
	b2.Release()
	b3.Release()
	if b.InUse() != 0 {
		t.Fatalf("inUse = %d", b.InUse())
	}
}

func TestGetHonoursContext(t *testing.T) {
	b := NewBudget(1 << 10)
	p := NewPool(1<<10, b)
	held, _ := p.Get(context.Background())
	defer held.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := p.Get(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline error, got %v", err)
	}
}

func TestSharedBudget(t *testing.T) {
	b := NewBudget(3 << 10)
	small := NewPool(1<<10, b)
	big := NewPool(2<<10, b)
	s, _ := small.Get(context.Background())
	g, _ := big.Get(context.Background())
	if _, ok := small.TryGet(); ok {
		t.Fatal("budget shared across pools must be exhausted")
	}
	s.Release()
	g.Release()
}

func TestAlignment(t *testing.T) {
	b := NewBudget(1 << 20)
	p := NewPool(8<<10, b, WithAlignment(4096))
	for i := 0; i < 32; i++ {
		buf, _ := p.Get(context.Background())
		if addr := uintptr(unsafe.Pointer(&buf.Bytes()[0])); addr%4096 != 0 {
			t.Fatalf("buffer at %#x not aligned", addr)
		}
		if len(buf.Bytes()) != 8<<10 || cap(buf.Bytes()) != 8<<10 {
			t.Fatal("aligned buffer has wrong len/cap")
		}
		buf.Release()
	}
}

func TestConcurrent(t *testing.T) {
	b := NewBudget(8 << 10)
	p := NewPool(1<<10, b)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				buf, err := p.Get(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				buf.Bytes()[0] = byte(j)
				buf.Release()
			}
		}()
	}
	wg.Wait()
	if b.InUse() != 0 {
		t.Fatalf("inUse = %d", b.InUse())
	}
}

func BenchmarkGetRelease(b *testing.B) {
	p := NewPool(1<<20, NewBudget(64<<20))
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf, _ := p.Get(ctx)
			buf.Release()
		}
	})
}

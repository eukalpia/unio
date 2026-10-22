package rs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/ids"
)

// frameBytes is the budget of one stripe frame of a k+m set.
func frameBytes(k, m, stripe int) int64 {
	return int64((k + m) * bitrot.ShardBlockSize(stripe, k))
}

// budgetCtx bounds an operation that must make progress on a tight budget. A
// stalled pipeline fails after the deadline instead of at the test timeout.
func budgetCtx(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// readObject reads a whole object under ctx and closes the reader.
func readObject(ctx context.Context, s *Set, id ids.DataID, layout erasure.Layout, size int64) ([]byte, error) {
	rc, err := s.Read(ctx, id, layout, size, 0, size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// TestNewAcceptsBudgetOfOneFrameAndRejectsLess: one frame is the smallest
// budget a set can run on, and a budget below it is refused up front.
func TestNewAcceptsBudgetOfOneFrameAndRejectsLess(t *testing.T) {
	const stripe = 64 << 10
	frame := frameBytes(2, 1, stripe)
	if _, err := New(ids.SetID(1), drivesOf(3), 2, 1, Options{StripeSize: stripe, Budget: bufpool.NewBudget(frame)}); err != nil {
		t.Fatalf("budget of one frame refused: %v", err)
	}
	if _, err := New(ids.SetID(1), drivesOf(3), 2, 1, Options{StripeSize: stripe, Budget: bufpool.NewBudget(frame - 1)}); err == nil {
		t.Fatal("budget smaller than one frame accepted")
	}
}

// TestReadCompletesWithBudgetOfOneFrame: a multi-stripe read must progress with
// a single frame of budget, one stripe at a time.
func TestReadCompletesWithBudgetOfOneFrame(t *testing.T) {
	const stripe = 64 << 10
	budget := bufpool.NewBudget(frameBytes(2, 1, stripe))
	s, _ := newTestSet(t, 2, 1, Options{StripeSize: stripe, Budget: budget})
	data := pseudo(6*stripe+123, 41)
	id, layout := mustWrite(t, s, data)

	got, err := readObject(budgetCtx(t, 5*time.Second), s, id, layout, int64(len(data)))
	if err != nil {
		t.Fatalf("read with a budget of one frame: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("read with a budget of one frame returned wrong bytes")
	}
}

// TestConcurrentReadsOnBudgetOfNFrames: N readers open on one set, and all N
// must then drain, on a budget of N frames and also of a single frame. Opening
// a reader must not wait for budget held by the readers already open.
func TestConcurrentReadsOnBudgetOfNFrames(t *testing.T) {
	for _, frames := range []int{4, 1} {
		t.Run(fmt.Sprintf("budget=%d_frames", frames), func(t *testing.T) {
			concurrentReads(t, 4, frames)
		})
	}
}

func concurrentReads(t *testing.T, n, frames int) {
	t.Helper()
	const stripe = 64 << 10
	budget := bufpool.NewBudget(int64(frames) * frameBytes(2, 1, stripe))
	s, _ := newTestSet(t, 2, 1, Options{StripeSize: stripe, Budget: budget})
	data := pseudo(5*stripe+7, 42)
	id, layout := mustWrite(t, s, data)
	size := int64(len(data))

	ctx := budgetCtx(t, 5*time.Second)
	readers := make([]io.ReadCloser, n)
	for i := range readers {
		rc, err := s.Read(ctx, id, layout, size, 0, size)
		if err != nil {
			t.Fatalf("open reader %d of %d on a budget of %d frames: %v", i+1, n, n, err)
		}
		readers[i] = rc
	}

	errs := make(chan error, n)
	var wg sync.WaitGroup
	for _, rc := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := io.ReadAll(rc)
			if cerr := rc.Close(); err == nil {
				err = cerr
			}
			if err == nil && !bytes.Equal(got, data) {
				err = errors.New("wrong bytes")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("one of %d concurrent reads on a budget of %d frames: %v", n, frames, err)
		}
	}
	if budget.InUse() != 0 {
		t.Fatalf("%d bytes still in use after the reads", budget.InUse())
	}
}

// TestWriteAndHealCompleteWithBudgetOfOneFrame: the same tight budget must
// also carry a write, a heal that verifies every shard before rebuilding, and
// a read of the healed object.
func TestWriteAndHealCompleteWithBudgetOfOneFrame(t *testing.T) {
	const stripe = 64 << 10
	budget := bufpool.NewBudget(frameBytes(2, 1, stripe))
	s, mem := newTestSet(t, 2, 1, Options{StripeSize: stripe, Budget: budget})
	data := pseudo(4*stripe+9, 43)
	size := int64(len(data))
	ctx := budgetCtx(t, 10*time.Second)
	id := ids.NewDataID()
	layout, err := s.Write(ctx, id, bytes.NewReader(data), size)
	if err != nil {
		t.Fatalf("write with a budget of one frame: %v", err)
	}
	if err := mem[0].Corrupt(shardID(id, 0), 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heal(ctx, id, layout, size); err != nil {
		t.Fatalf("heal with a budget of one frame: %v", err)
	}
	got, err := readObject(ctx, s, id, layout, size)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read after heal: err=%v", err)
	}
}

package engine_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/engine/internal/testbackend"
	"github.com/eukalpia/unio/internal/object/objecttest"
)

// deleteFailingSet fails every deletion.
type deleteFailingSet struct{ erasure.Set }

func (s deleteFailingSet) Delete(context.Context, ids.DataID, erasure.Layout) error {
	return errors.New("induced deletion failure")
}

// gatedDeleteSet holds every deletion until gate is closed.
type gatedDeleteSet struct {
	erasure.Set
	gate <-chan struct{}
}

func (s gatedDeleteSet) Delete(ctx context.Context, id ids.DataID, layout erasure.Layout) error {
	select {
	case <-s.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.Set.Delete(ctx, id, layout)
}

// putErasure stores a payload of the erasure tier under key.
func putErasure(t *testing.T, h *harness, key string, seed byte) {
	t.Helper()
	if _, err := h.PutObject(t.Context(), testBucket, key, objecttest.NewBody(payloadOf(5000, seed)), object.PutOptions{}); err != nil {
		t.Fatalf("put %q: %v", key, err)
	}
}

// TestStatsReportsCompletedDeletions overwrites one key three times. The first
// put replaces nothing, so two deletions are queued and both succeed.
func TestStatsReportsCompletedDeletions(t *testing.T) {
	h := newHarness(t, engine.Options{})
	if _, err := h.MakeBucket(t.Context(), testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		putErasure(t, h, "k", byte(i))
	}
	if err := h.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, want := h.Stats(), (engine.Stats{GCCompleted: 2}); got != want {
		t.Fatalf("Stats() = %+v, want %+v", got, want)
	}
}

// TestStatsReportsFailedDeletions makes every deletion fail. The data is kept
// for the scrubber, and the failures are counted.
func TestStatsReportsFailedDeletions(t *testing.T) {
	reg := testbackend.NewRegistry(2, 2, 1)
	sets := wrappedRegistry{Registry: reg, wrap: func(s erasure.Set) erasure.Set { return deleteFailingSet{s} }}
	h := newHarnessWith(t, testbackend.NewStore(), reg, sets, engine.Options{})
	if _, err := h.MakeBucket(t.Context(), testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	putErasure(t, h, "k", 1)
	putErasure(t, h, "k", 2)
	if err := h.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, want := h.Stats(), (engine.Stats{GCFailed: 1}); got != want {
		t.Fatalf("Stats() = %+v, want %+v", got, want)
	}
}

// TestStatsReportsPendingAndDroppedDeletions holds every deletion with a
// backlog of one. Of three replaced versions, at most two can be accepted, and
// the rest are dropped. Nothing finishes while the gate is closed.
func TestStatsReportsPendingAndDroppedDeletions(t *testing.T) {
	gate := make(chan struct{})
	reg := testbackend.NewRegistry(2, 2, 1)
	sets := wrappedRegistry{Registry: reg, wrap: func(s erasure.Set) erasure.Set { return gatedDeleteSet{Set: s, gate: gate} }}
	h := newHarnessWith(t, testbackend.NewStore(), reg, sets, engine.Options{GCWorkers: 1, GCQueueSize: 1})
	if _, err := h.MakeBucket(t.Context(), testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		putErasure(t, h, "k", byte(i))
	}
	s := h.Stats()
	if s.GCPending+int(s.GCDropped) != 3 || s.GCDropped == 0 || s.GCPending > 2 {
		t.Fatalf("Stats() = %+v with the gate closed, want pending+dropped = 3, dropped >= 1 and pending <= 2", s)
	}
	accepted := s.GCPending

	close(gate)
	if err := h.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := engine.Stats{GCCompleted: uint64(accepted), GCDropped: s.GCDropped}
	if got := h.Stats(); got != want {
		t.Fatalf("Stats() after the flush = %+v, want %+v", got, want)
	}
}

// TestStatsIsSafeWhileDeletionsRun reads the counters while writers replace
// objects. Under -race this also checks the synchronisation of the queue.
func TestStatsIsSafeWhileDeletionsRun(t *testing.T) {
	h := newHarness(t, engine.Options{GCWorkers: 2})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	const writers, overwrites = 4, 10

	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		var last uint64
		for {
			select {
			case <-stop:
				return
			default:
			}
			s := h.Stats()
			if s.GCCompleted < last {
				t.Errorf("GCCompleted went from %d to %d", last, s.GCCompleted)
				return
			}
			last = s.GCCompleted
			runtime.Gosched()
		}
	}()

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("k-%d", w)
			for i := range overwrites {
				body := objecttest.NewBody(payloadOf(5000, byte(i)))
				if _, err := h.PutObject(ctx, testBucket, key, body, object.PutOptions{}); err != nil {
					t.Errorf("put %q: %v", key, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := h.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	close(stop)
	readers.Wait()

	if got, want := h.Stats(), (engine.Stats{GCCompleted: writers * (overwrites - 1)}); got != want {
		t.Fatalf("Stats() = %+v, want %+v", got, want)
	}
}

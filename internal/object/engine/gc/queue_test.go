package gc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// recordingSet is an erasure.Set whose Delete calls are recorded. When gate
// is non-nil, Delete blocks until gate is closed.
type recordingSet struct {
	gate    chan struct{}
	failErr error
	mu      sync.Mutex
	deleted []ids.DataID
}

func (s *recordingSet) ID() ids.SetID     { return 1 }
func (s *recordingSet) Shape() (int, int) { return 2, 1 }
func (s *recordingSet) Heal(context.Context, ids.DataID, erasure.Layout, int64) (erasure.Layout, error) {
	return erasure.Layout{}, nil
}
func (s *recordingSet) Write(context.Context, ids.DataID, io.Reader, int64) (erasure.Layout, error) {
	return erasure.Layout{}, errors.New("not used")
}
func (s *recordingSet) Read(context.Context, ids.DataID, erasure.Layout, int64, int64, int64) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}

func (s *recordingSet) Delete(ctx context.Context, id ids.DataID, _ erasure.Layout) error {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.failErr != nil {
		return s.failErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *recordingSet) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleted)
}

func newQueue(t *testing.T, cfg Config) *Queue {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := q.Close(context.Background()); err != nil {
			t.Errorf("close queue: %v", err)
		}
	})
	return q
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []Config{
		{QueueSize: 0, Workers: 1},
		{QueueSize: 1, Workers: 0},
		{QueueSize: -1, Workers: 1},
	}
	for _, cfg := range tests {
		if _, err := New(cfg); err == nil {
			t.Fatalf("New(%+v): want error", cfg)
		}
	}
}

func TestWaitDrainsEnqueuedDeletions(t *testing.T) {
	q := newQueue(t, Config{QueueSize: 64, Workers: 4})
	set := &recordingSet{}
	const n = 32
	for i := 0; i < n; i++ {
		if !q.Enqueue(Task{Set: set, Data: ids.NewDataID()}) {
			t.Fatalf("enqueue %d rejected", i)
		}
	}
	if err := q.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := set.count(); got != n {
		t.Fatalf("deleted %d, want %d", got, n)
	}
	if got := q.Completed(); got != n {
		t.Fatalf("Completed() = %d, want %d", got, n)
	}
}

func TestEnqueueNeverBlocksAndDropsOnOverflow(t *testing.T) {
	const workers, size, total = 1, 2, 10
	gate := make(chan struct{})
	set := &recordingSet{gate: gate}
	q := newQueue(t, Config{QueueSize: size, Workers: workers})
	accepted := 0
	for i := 0; i < total; i++ {
		if q.Enqueue(Task{Set: set, Data: ids.NewDataID()}) {
			accepted++
		}
	}
	// At most one task is held by the blocked worker and size wait in the
	// channel; everything else is dropped.
	if accepted > workers+size {
		t.Fatalf("accepted %d tasks, capacity is %d", accepted, workers+size)
	}
	if got := q.Dropped(); got != uint64(total-accepted) {
		t.Fatalf("Dropped() = %d, want %d", got, total-accepted)
	}
	close(gate)
	if err := q.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := set.count(); got != accepted {
		t.Fatalf("deleted %d, accepted %d", got, accepted)
	}
}

func TestRejectsNilSetAndZeroData(t *testing.T) {
	q := newQueue(t, Config{QueueSize: 4, Workers: 1})
	if q.Enqueue(Task{Data: ids.NewDataID()}) {
		t.Fatal("task without a set was accepted")
	}
	if q.Enqueue(Task{Set: &recordingSet{}}) {
		t.Fatal("task with zero data id was accepted")
	}
}

func TestFailedDeletionIsCountedAndDropped(t *testing.T) {
	q := newQueue(t, Config{QueueSize: 4, Workers: 1})
	bad := &recordingSet{failErr: errors.New("drive offline")}
	if !q.Enqueue(Task{Set: bad, Data: ids.NewDataID()}) {
		t.Fatal("enqueue rejected")
	}
	if err := q.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q.Failed() != 1 || q.Completed() != 0 {
		t.Fatalf("failed=%d done=%d, want 1 and 0", q.Failed(), q.Completed())
	}
}

func TestCloseDrainsAndRejectsLaterWork(t *testing.T) {
	q, err := New(Config{QueueSize: 16, Workers: 2, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	set := &recordingSet{}
	for i := 0; i < 8; i++ {
		q.Enqueue(Task{Set: set, Data: ids.NewDataID()})
	}
	if err := q.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := set.count(); got != 8 {
		t.Fatalf("Close drained %d deletions, want 8", got)
	}
	if q.Enqueue(Task{Set: set, Data: ids.NewDataID()}) {
		t.Fatal("enqueue after Close was accepted")
	}
	if err := q.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseStopsWhenContextIsDone(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	set := &recordingSet{gate: gate}
	q, err := New(Config{QueueSize: 4, Workers: 1, Timeout: time.Minute, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	q.Enqueue(Task{Set: set, Data: ids.NewDataID()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := q.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close with done context: err = %v, want context.Canceled", err)
	}
}

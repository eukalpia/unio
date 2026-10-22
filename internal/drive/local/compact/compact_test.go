package compact

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// fakeTarget is an in-memory Target that records what compaction did.
type fakeTarget struct {
	mu       sync.Mutex
	segments []Segment
	records  map[uint64][]Record
	moved    []Record
	retired  []uint64
	gone     map[uint64]bool // Visit fails with ErrGone
	moveErr  error
	live     map[uint64]bool // Retire fails with ErrLive
	retireCh chan uint64
}

func (f *fakeTarget) Sealed() []Segment {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Segment(nil), f.segments...)
}

func (f *fakeTarget) Visit(_ context.Context, seg uint64, fn func(Record) error) error {
	f.mu.Lock()
	gone := f.gone[seg]
	recs := f.records[seg]
	f.mu.Unlock()
	if gone {
		return ErrGone
	}
	for _, r := range recs {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeTarget) Move(_ context.Context, rec Record) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.moveErr != nil {
		return 0, f.moveErr
	}
	f.moved = append(f.moved, rec)
	return segment.RecordSize(int64(rec.Header.Length)), nil
}

func (f *fakeTarget) Retire(_ context.Context, seg uint64) error {
	f.mu.Lock()
	if f.live[seg] {
		f.mu.Unlock()
		return ErrLive
	}
	f.retired = append(f.retired, seg)
	f.mu.Unlock()
	if f.retireCh != nil {
		f.retireCh <- seg
	}
	return nil
}

func rec(seg uint64, off int64, n uint64) Record {
	return Record{Segment: seg, Offset: off, Header: segment.Header{ShardID: ids.ShardID{Index: uint16(off)}, Length: n}}
}

func TestPassRetiresOnlyMostlyDeadSegmentsOldestFirst(t *testing.T) {
	f := &fakeTarget{
		segments: []Segment{
			{ID: 1, End: 1000, Live: 100}, // 90% dead
			{ID: 2, End: 1000, Live: 600}, // 40% dead: kept
			{ID: 3, End: 1000, Live: 0},   // 100% dead
			{ID: 4, End: 0},               // empty
		},
		records: map[uint64][]Record{
			1: {rec(1, 0, 100)},
			3: {rec(3, 0, 10), rec(3, 60, 20)},
		},
	}
	n, err := Pass(context.Background(), f, Config{})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if n != 2 || !reflect.DeepEqual(f.retired, []uint64{1, 3}) {
		t.Fatalf("retired %v (n=%d), want [1 3]", f.retired, n)
	}
	if len(f.moved) != 3 {
		t.Fatalf("moved %d records, want 3", len(f.moved))
	}
}

func TestPassSkipsGoneAndLiveSegments(t *testing.T) {
	f := &fakeTarget{
		segments: []Segment{{ID: 1, End: 100, Live: 0}, {ID: 2, End: 100, Live: 0}},
		gone:     map[uint64]bool{1: true},
		live:     map[uint64]bool{2: true},
	}
	n, err := Pass(context.Background(), f, Config{})
	if err != nil || n != 0 {
		t.Fatalf("pass = %d, %v; want 0 retired, no error", n, err)
	}
}

func TestPassStopsOnMoveError(t *testing.T) {
	boom := errors.New("disk on fire")
	f := &fakeTarget{
		segments: []Segment{{ID: 1, End: 100, Live: 0}},
		records:  map[uint64][]Record{1: {rec(1, 0, 5)}},
		moveErr:  boom,
	}
	_, err := Pass(context.Background(), f, Config{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if len(f.retired) != 0 {
		t.Fatal("segment retired after a failed move")
	}
}

func TestPassHonoursCancellation(t *testing.T) {
	f := &fakeTarget{segments: []Segment{{ID: 1, End: 100}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Pass(ctx, f, Config{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRunWakesAndStopsOnCancel(t *testing.T) {
	defer goleak.VerifyNone(t)
	f := &fakeTarget{
		segments: []Segment{{ID: 7, End: 100, Live: 0}},
		retireCh: make(chan uint64, 1),
	}
	wake := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, f, Config{Wake: wake, Interval: time.Hour})
	}()

	wake <- struct{}{}
	if got := <-f.retireCh; got != 7 {
		t.Fatalf("retired %d, want 7", got)
	}
	cancel()
	<-done
}

func TestLimiterReservations(t *testing.T) {
	start := time.Unix(1000, 0)
	now := start
	l := &Limiter{rate: 1000, burst: 1000, tokens: 1000, last: start, now: func() time.Time { return now }}

	if d := l.reserve(1000); d != 0 {
		t.Fatalf("full burst should be free, waited %v", d)
	}
	if d := l.reserve(500); d != 500*time.Millisecond {
		t.Fatalf("500 bytes after empty bucket: wait %v, want 500ms", d)
	}
	now = now.Add(time.Second) // refill is capped at the burst
	if d := l.reserve(1000); d != 500*time.Millisecond {
		t.Fatalf("after 1s refill, 1000 bytes with 500 debt: wait %v, want 500ms", d)
	}
}

func TestLimiterUnlimitedAndCancel(t *testing.T) {
	var unlimited *Limiter
	if err := unlimited.Wait(context.Background(), 1<<30); err != nil {
		t.Fatalf("nil limiter: %v", err)
	}
	l := NewLimiter(1, 1) // one byte per second
	if err := l.Wait(context.Background(), 1); err != nil {
		t.Fatalf("first token: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Wait(ctx, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: err = %v", err)
	}
}

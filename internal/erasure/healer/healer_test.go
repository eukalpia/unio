package healer

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/erasuretest"
	"github.com/eukalpia/unio/internal/erasure/rs"
	"github.com/eukalpia/unio/internal/ids"
)

// writeObject stores data in s and returns its id and layout.
func writeObject(t *testing.T, s *rs.Set, data []byte) (ids.DataID, erasure.Layout) {
	t.Helper()
	id := ids.NewDataID()
	layout, err := s.Write(context.Background(), id, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return id, layout
}

// readObject returns the whole object stored under id.
func readObject(t *testing.T, s *rs.Set, id ids.DataID, layout erasure.Layout, size int64) []byte {
	t.Helper()
	rc, err := s.Read(context.Background(), id, layout, size, 0, size)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	return got
}

// TestHealRepairsDamagedShard damages one shard of an object, once by deleting
// it and once by corrupting it. The healer must rebuild it byte for byte, and
// the object must read back with every shard present.
func TestHealRepairsDamagedShard(t *testing.T) {
	const victim = 1
	cases := []struct {
		name   string
		damage func(t *testing.T, d *erasuretest.Drive, sid ids.ShardID)
	}{
		{"missing", func(t *testing.T, d *erasuretest.Drive, sid ids.ShardID) {
			if err := d.DeleteShard(context.Background(), sid); err != nil {
				t.Fatalf("DeleteShard: %v", err)
			}
		}},
		{"corrupt", func(t *testing.T, d *erasuretest.Drive, sid ids.ShardID) {
			if err := d.Corrupt(sid, 100); err != nil {
				t.Fatalf("Corrupt: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, mem := newSet(t, 7, 2, 1, rs.Options{})
			data := pseudo(300<<10, 5)
			id, layout := writeObject(t, s, data)
			size := int64(len(data))
			sid := ids.ShardID{Data: id, Index: victim}
			original, ok := mem[victim].Shard(sid)
			if !ok {
				t.Fatal("victim shard was not stored")
			}
			tc.damage(t, mem[victim], sid)

			s.HealQueue().Push(rs.HealRequest{Data: id, Layout: layout, Size: size, Shard: victim})
			var logs bytes.Buffer
			h := New([]*rs.Set{s}, Options{Workers: 2, Logger: textLogger(&logs)})
			runUntil(t, h, func() bool { return h.Stats().Healed == 1 })

			if st := h.Stats(); st.Failed != 0 || st.Pending != 0 {
				t.Fatalf("stats after repair: %+v", st)
			}
			for i, d := range mem {
				if _, ok := d.Shard(ids.ShardID{Data: id, Index: uint16(i)}); !ok {
					t.Fatalf("shard %d missing after repair", i)
				}
			}
			if got, _ := mem[victim].Shard(sid); !bytes.Equal(got, original) {
				t.Fatal("repaired shard differs from the original bytes")
			}
			if got := readObject(t, s, id, layout, size); !bytes.Equal(got, data) {
				t.Fatal("object reads back wrong after repair")
			}
			if strings.Contains(logs.String(), "level=WARN") {
				t.Fatalf("a plain repair logged a warning:\n%s", logs.String())
			}
		})
	}
}

// TestHealHealthyObjectCountsAsHealed checks that a report for an object with
// nothing to repair is a successful heal.
func TestHealHealthyObjectCountsAsHealed(t *testing.T) {
	s, _ := newSet(t, 3, 2, 1, rs.Options{})
	id, layout := writeObject(t, s, pseudo(1000, 9))
	s.HealQueue().Push(rs.HealRequest{Data: id, Layout: layout, Size: 1000, Shard: 0})
	h := New([]*rs.Set{s}, Options{Workers: 1})
	runUntil(t, h, func() bool { return h.Stats().Healed == 1 })
	if st := h.Stats(); st.Failed != 0 {
		t.Fatalf("stats: %+v, want no failures", st)
	}
}

// TestHealFailureIsCounted checks that a heal that cannot succeed is counted as
// failed and logged. Losing two of three shards with one parity leaves less than
// the data shards needed.
func TestHealFailureIsCounted(t *testing.T) {
	s, mem := newSet(t, 4, 2, 1, rs.Options{})
	data := pseudo(4096, 11)
	id, layout := writeObject(t, s, data)
	for _, i := range []int{0, 1} {
		if err := mem[i].DeleteShard(context.Background(), ids.ShardID{Data: id, Index: uint16(i)}); err != nil {
			t.Fatalf("DeleteShard %d: %v", i, err)
		}
	}
	s.HealQueue().Push(rs.HealRequest{Data: id, Layout: layout, Size: int64(len(data)), Shard: 0})
	var logs bytes.Buffer
	h := New([]*rs.Set{s}, Options{Workers: 1, Logger: textLogger(&logs)})
	runUntil(t, h, func() bool { return h.Stats().Failed == 1 })
	if st := h.Stats(); st.Healed != 0 {
		t.Fatalf("stats: %+v, want no successful heal", st)
	}
	if !strings.Contains(logs.String(), "heal failed") {
		t.Fatalf("failure not logged:\n%s", logs.String())
	}
}

// TestHealTimeoutIsCounted checks that the per-heal timeout applies. A healthy
// object heals under the default timeout, but fails under a timeout that has
// already passed.
func TestHealTimeoutIsCounted(t *testing.T) {
	s, _ := newSet(t, 5, 2, 1, rs.Options{})
	id, layout := writeObject(t, s, pseudo(4096, 13))
	s.HealQueue().Push(rs.HealRequest{Data: id, Layout: layout, Size: 4096, Shard: 0})
	h := New([]*rs.Set{s}, Options{Workers: 1, Timeout: time.Nanosecond})
	runUntil(t, h, func() bool { return h.Stats().Failed == 1 })
	if st := h.Stats(); st.Healed != 0 {
		t.Fatalf("stats: %+v, want the heal to time out", st)
	}
}

// TestHealChangedLayoutIsLogged covers the case where the heal returns a layout
// that differs from the request. The request records no drive for shard 2 while
// the shard exists, so the heal rewrites it on the set's drive and records that
// drive. The heal succeeds, and the change must be logged at Warn.
func TestHealChangedLayoutIsLogged(t *testing.T) {
	s, mem := newSet(t, 6, 2, 1, rs.Options{})
	data := pseudo(4096, 17)
	id, layout := writeObject(t, s, data)
	layout.Drives[2] = ids.DriveID{}
	s.HealQueue().Push(rs.HealRequest{Data: id, Layout: layout, Size: int64(len(data)), Shard: 2})
	var logs bytes.Buffer
	h := New([]*rs.Set{s}, Options{Workers: 1, Logger: textLogger(&logs)})
	runUntil(t, h, func() bool { return h.Stats().Healed == 1 })
	if !strings.Contains(logs.String(), "heal changed the object layout") {
		t.Fatalf("layout change not logged:\n%s", logs.String())
	}
	if _, ok := mem[2].Shard(ids.ShardID{Data: id, Index: 2}); !ok {
		t.Fatal("shard 2 missing after repair")
	}
}

// TestDuplicateReportsHealOnce reports the same object five times while its
// heal is slow. The duplicates must be skipped while the first heal is in
// flight, so the object is healed once.
func TestDuplicateReportsHealOnce(t *testing.T) {
	s, mem := newSet(t, 8, 2, 1, rs.Options{})
	data := pseudo(64<<10, 19)
	id, layout := writeObject(t, s, data)
	size := int64(len(data))
	for _, d := range mem {
		d.SetFaults(erasuretest.Faults{Latency: 100 * time.Millisecond})
	}
	for range 5 {
		s.HealQueue().Push(rs.HealRequest{Data: id, Layout: layout, Size: size, Shard: 0})
	}

	var logs bytes.Buffer
	h := New([]*rs.Set{s}, Options{Workers: 1, Logger: textLogger(&logs)})
	runUntil(t, h, func() bool { return h.Stats().Healed >= 1 && h.Stats().Pending == 0 })
	if st := h.Stats(); st.Healed != 1 || st.Failed != 0 {
		t.Fatalf("stats: %+v, want exactly one heal", st)
	}
	if n := strings.Count(logs.String(), "heal request already in flight"); n != 4 {
		t.Fatalf("skipped %d duplicates, want 4:\n%s", n, logs.String())
	}
}

// TestSetsShareWorkers checks that one busy set does not starve another. The
// busy set has 50 reports queued ahead of one report from the quiet set. With a
// single worker, the quiet report must be healed among the first few, not after
// all 50. Each heal fails at once because its object does not exist, so the test
// measures scheduling and not repair time.
func TestSetsShareWorkers(t *testing.T) {
	busy, busyMem := newSet(t, 1, 2, 1, rs.Options{HealQueueSize: 64})
	quiet, quietMem := newSet(t, 2, 2, 1, rs.Options{})
	for range 50 {
		busy.HealQueue().Push(rs.HealRequest{Data: ids.NewDataID(), Layout: layoutOf(busy, busyMem), Size: 10})
	}
	quiet.HealQueue().Push(rs.HealRequest{Data: ids.NewDataID(), Layout: layoutOf(quiet, quietMem), Size: 10})

	var logs bytes.Buffer
	h := New([]*rs.Set{busy, quiet}, Options{Workers: 1, Logger: textLogger(&logs)})
	runUntil(t, h, func() bool { return h.Stats().Failed == 51 })

	position := 0
	for _, line := range strings.Split(logs.String(), "\n") {
		if !strings.Contains(line, "heal failed") {
			continue
		}
		position++
		if strings.Contains(line, "set=2") {
			break
		}
	}
	if position == 0 || position > 4 {
		t.Fatalf("the quiet set was healed at position %d of 51, want one of the first four", position)
	}
	if st := h.Stats(); st.Pending != 0 {
		t.Fatalf("stats: %+v, want nothing pending", st)
	}
}

// TestDroppedReportsAreCounted checks that Stats sums the drops of every queue.
// A full queue drops the report and counts it; the queue keeps what it accepted.
func TestDroppedReportsAreCounted(t *testing.T) {
	s, mem := newSet(t, 9, 2, 1, rs.Options{HealQueueSize: 1})
	layout := layoutOf(s, mem)
	for range 3 {
		s.HealQueue().Push(rs.HealRequest{Data: ids.NewDataID(), Layout: layout, Size: 10})
	}
	h := New([]*rs.Set{s}, Options{})
	st := h.Stats()
	if st.Dropped != 2 || st.Pending != 1 {
		t.Fatalf("stats: %+v, want 2 dropped and 1 pending", st)
	}
}

// TestRunStopsWithoutLeaks cancels Run while it has requests queued and
// handed off, and while Stats is read concurrently. Run must return, every
// goroutine must exit (goleak), and Pending must count only what is still in
// a queue.
func TestRunStopsWithoutLeaks(t *testing.T) {
	defer goleak.VerifyNone(t)
	s, mem := newSet(t, 10, 2, 1, rs.Options{})
	layout := layoutOf(s, mem)
	for range 10 {
		s.HealQueue().Push(rs.HealRequest{Data: ids.NewDataID(), Layout: layout, Size: 10})
	}
	h := New([]*rs.Set{s}, Options{Workers: 3})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Run(ctx)
	}()
	readerDone := make(chan struct{})
	stopReader := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stopReader:
				return
			default:
				_ = h.Stats()
			}
		}
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	close(stopReader)
	<-readerDone

	if got, want := h.Stats().Pending, s.HealQueue().Len(); got != want {
		t.Fatalf("Pending = %d after Run returned, want the %d reports still in the queue", got, want)
	}
}

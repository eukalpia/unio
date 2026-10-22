package logstore

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/hashicorp/raft"
)

// TestConcurrentGroupsStayConsistent runs, for each of several groups at
// once, one log writer, two readers of the bounds and entries, and one writer
// of stable values. Readers only see states the writer produced, and the
// final contents are exact.
func TestConcurrentGroupsStayConsistent(t *testing.T) {
	const (
		groups  = 6
		entries = 200
		batch   = 10
		reads   = 200
		keys    = 50
	)
	s := newTestStore(t)
	var wg sync.WaitGroup
	// Each goroutine sends at most one error and then returns.
	errs := make(chan error, groups*4)

	for g := range groups {
		id := ids.PartitionID(g + 1)
		group := s.Group(id)
		tag := fmt.Sprintf("g%d", id)

		wg.Go(func() {
			for lo := uint64(1); lo <= entries; lo += batch {
				if err := group.StoreLogs(taggedLogs(tag, lo, lo+batch-1)); err != nil {
					errs <- fmt.Errorf("group %d: StoreLogs(%d): %w", id, lo, err)
					return
				}
			}
		})

		for range 2 {
			wg.Go(func() {
				for range reads {
					first, err := group.FirstIndex()
					if err != nil {
						errs <- fmt.Errorf("group %d: FirstIndex: %w", id, err)
						return
					}
					last, err := group.LastIndex()
					if err != nil {
						errs <- fmt.Errorf("group %d: LastIndex: %w", id, err)
						return
					}
					if first > 1 || last > entries {
						errs <- fmt.Errorf("group %d: bounds %d..%d were never written", id, first, last)
						return
					}
					if last == 0 {
						continue
					}
					var l raft.Log
					if err := group.GetLog(last, &l); err != nil {
						errs <- fmt.Errorf("group %d: GetLog(%d): %w", id, last, err)
						return
					}
				}
			})
		}

		wg.Go(func() {
			for k := range keys {
				if err := group.SetUint64([]byte(fmt.Sprintf("n-%d", k)), uint64(k)); err != nil {
					errs <- fmt.Errorf("group %d: SetUint64: %w", id, err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	for g := range groups {
		id := ids.PartitionID(g + 1)
		group := s.Group(id)
		assertBounds(t, group, 1, entries)
		assertLogs(t, group, taggedLogs(fmt.Sprintf("g%d", id), 1, entries))
		for k := range keys {
			v, err := group.GetUint64([]byte(fmt.Sprintf("n-%d", k)))
			if err != nil || v != uint64(k) {
				t.Fatalf("group %d key n-%d = %d, %v; want %d", id, k, v, err, k)
			}
		}
	}
}

// TestRacingAppendsToOneGroupAcceptExactlyOne releases many goroutines at
// once, all appending index 11 to the same group. The group serializes the
// writes, so exactly one append succeeds and the others see a non-contiguous
// index.
func TestRacingAppendsToOneGroupAcceptExactlyOne(t *testing.T) {
	const racers = 16
	s := newTestStore(t)
	g := s.Group(1)
	mustStoreLogs(t, g, logsRange(1, 10))

	var (
		wg   sync.WaitGroup
		wins atomic.Int32
		gate = make(chan struct{})
	)
	for i := range racers {
		wg.Go(func() {
			<-gate
			err := g.StoreLog(&raft.Log{Index: 11, Term: 2, Data: fmt.Appendf(nil, "racer-%d", i)})
			if err == nil {
				wins.Add(1)
			}
		})
	}
	close(gate)
	wg.Wait()

	if n := wins.Load(); n != 1 {
		t.Fatalf("%d racers appended index 11, want exactly 1", n)
	}
	assertBounds(t, g, 1, 11)
	var l raft.Log
	if err := g.GetLog(11, &l); err != nil {
		t.Fatalf("GetLog(11): %v", err)
	}
	if !bytes.HasPrefix(l.Data, []byte("racer-")) {
		t.Fatalf("index 11 holds %q, want a racer's entry", l.Data)
	}
}

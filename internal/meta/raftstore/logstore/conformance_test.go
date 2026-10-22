package logstore

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/hashicorp/raft"
)

// newTestStore opens a store in a fresh directory with a test-owned cache.
// The store is closed when the test ends; the second Close is a no-op.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	cache := pebble.NewCache(16 << 20)
	t.Cleanup(cache.Unref)
	s, err := Open(t.TempDir(), cache)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

// logsRange returns entries lo..hi (inclusive) with terms and data that name
// their index, so that a misplaced entry is visible in the assertions.
func logsRange(lo, hi uint64) []*raft.Log {
	return taggedLogs("entry", lo, hi)
}

// taggedLogs is logsRange with a caller-chosen data prefix, so entries of
// different groups with the same indices can be told apart.
func taggedLogs(tag string, lo, hi uint64) []*raft.Log {
	if hi < lo {
		return nil
	}
	logs := make([]*raft.Log, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		logs = append(logs, &raft.Log{
			Index: i,
			Term:  1 + i/10,
			Type:  raft.LogCommand,
			Data:  []byte(fmt.Sprintf("%s-%d", tag, i)),
		})
	}
	return logs
}

// assertBounds checks FirstIndex and LastIndex against the expected values.
func assertBounds(t *testing.T, g *GroupStore, wantFirst, wantLast uint64) {
	t.Helper()
	first, err := g.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex: %v", err)
	}
	last, err := g.LastIndex()
	if err != nil {
		t.Fatalf("LastIndex: %v", err)
	}
	if first != wantFirst || last != wantLast {
		t.Fatalf("bounds: got first %d last %d, want first %d last %d", first, last, wantFirst, wantLast)
	}
}

// assertMissing checks that GetLog reports raft.ErrLogNotFound for index.
func assertMissing(t *testing.T, g *GroupStore, index uint64) {
	t.Helper()
	var l raft.Log
	if err := g.GetLog(index, &l); !errors.Is(err, raft.ErrLogNotFound) {
		t.Fatalf("GetLog(%d): got %v, want raft.ErrLogNotFound", index, err)
	}
}

// assertLogs checks that GetLog returns exactly the entries of want.
func assertLogs(t *testing.T, g *GroupStore, want []*raft.Log) {
	t.Helper()
	for _, w := range want {
		var got raft.Log
		if err := g.GetLog(w.Index, &got); err != nil {
			t.Fatalf("GetLog(%d): %v", w.Index, err)
		}
		if !reflect.DeepEqual(got, *w) {
			t.Fatalf("GetLog(%d): got %+v, want %+v", w.Index, got, *w)
		}
	}
}

// TestRaftLogRoundTrip stores one entry per case and reads it back with
// reflect.DeepEqual, the comparison used by hashicorp's own log store tests.
func TestRaftLogRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 30, 15, 123456789, time.UTC)
	cases := []struct {
		name string
		log  raft.Log
	}{
		{"command with data", raft.Log{Index: 1, Term: 1, Type: raft.LogCommand, Data: []byte("log1")}},
		{"nil data and extensions", raft.Log{Index: 1, Term: 1}},
		{"empty but non-nil data", raft.Log{Index: 1, Term: 2, Data: []byte{}, Extensions: []byte{}}},
		{"every field set", raft.Log{Index: 1, Term: 7, Type: raft.LogConfiguration, Data: []byte{0, 1, 2}, Extensions: []byte("ext"), AppendedAt: at}},
		{"noop", raft.Log{Index: 1, Term: 3, Type: raft.LogNoop}},
		{"barrier", raft.Log{Index: 1, Term: 3, Type: raft.LogBarrier}},
		{"pre-epoch append time", raft.Log{Index: 1, Term: 1, AppendedAt: time.Date(1931, 3, 4, 5, 6, 7, 1, time.UTC)}},
		{"large data", raft.Log{Index: 1, Term: 1, Data: bytes.Repeat([]byte("x"), 1<<20)}},
		{"maximum index", raft.Log{Index: math.MaxUint64, Term: math.MaxUint64, Data: []byte("end")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestStore(t).Group(1)
			in := tc.log
			if err := g.StoreLog(&in); err != nil {
				t.Fatalf("StoreLog: %v", err)
			}
			var out raft.Log
			if err := g.GetLog(tc.log.Index, &out); err != nil {
				t.Fatalf("GetLog: %v", err)
			}
			if !reflect.DeepEqual(out, tc.log) {
				t.Fatalf("GetLog: got %+v, want %+v", out, tc.log)
			}
		})
	}
}

// TestEmptyGroupBounds checks the zero state: no logs means first and last
// index 0, and every index is missing.
func TestEmptyGroupBounds(t *testing.T) {
	g := newTestStore(t).Group(1)
	assertBounds(t, g, 0, 0)
	assertMissing(t, g, 1)
	assertMissing(t, g, 0)
}

// TestGetLogMissingIndex checks that absent indices, including the gap
// around a stored range, return raft.ErrLogNotFound.
func TestGetLogMissingIndex(t *testing.T) {
	g := newTestStore(t).Group(1)
	mustStoreLogs(t, g, logsRange(5, 7))
	for _, i := range []uint64{0, 1, 4, 8, math.MaxUint64} {
		assertMissing(t, g, i)
	}
}

// TestGetLogNilOutputIsError checks that a nil output pointer is an error,
// not a panic.
func TestGetLogNilOutputIsError(t *testing.T) {
	g := newTestStore(t).Group(1)
	mustStoreLogs(t, g, logsRange(1, 1))
	if err := g.GetLog(1, nil); err == nil {
		t.Fatal("GetLog with nil output returned nil error")
	}
}

// TestStoreLogsBounds checks FirstIndex and LastIndex after single and
// batched writes, and that an empty StoreLogs call changes nothing.
func TestStoreLogsBounds(t *testing.T) {
	g := newTestStore(t).Group(1)

	if err := g.StoreLogs(nil); err != nil {
		t.Fatalf("StoreLogs(nil) on empty group: %v", err)
	}
	if err := g.StoreLogs([]*raft.Log{}); err != nil {
		t.Fatalf("StoreLogs(empty) on empty group: %v", err)
	}
	assertBounds(t, g, 0, 0)

	mustStoreLogs(t, g, logsRange(1, 10))
	assertBounds(t, g, 1, 10)
	assertLogs(t, g, logsRange(1, 10))

	if err := g.StoreLogs([]*raft.Log{}); err != nil {
		t.Fatalf("StoreLogs(empty) on populated group: %v", err)
	}
	assertBounds(t, g, 1, 10)

	if err := g.StoreLog(&raft.Log{Index: 11, Term: 2, Data: []byte("single")}); err != nil {
		t.Fatalf("StoreLog: %v", err)
	}
	assertBounds(t, g, 1, 11)
}

// TestDeleteRange checks inclusive range deletes against FirstIndex,
// LastIndex and GetLog. Each index is expected to be missing exactly when it
// lies in [min, max].
func TestDeleteRange(t *testing.T) {
	cases := []struct {
		name                string
		min, max            uint64
		wantFirst, wantLast uint64
	}{
		{"prefix", 1, 3, 4, 10},
		{"suffix", 8, 10, 1, 7},
		{"interior", 4, 6, 1, 10},
		{"single first", 1, 1, 2, 10},
		{"single last", 10, 10, 1, 9},
		{"everything", 1, 10, 0, 0},
		{"range wider than the log", 0, 100, 0, 0},
		{"range above the log is a no-op", 11, 20, 1, 10},
		{"range below the log is a no-op", 0, 0, 1, 10},
		{"min above max is a no-op", 5, 4, 1, 10},
		{"max is the largest index", 5, math.MaxUint64, 1, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestStore(t).Group(1)
			mustStoreLogs(t, g, logsRange(1, 10))
			if err := g.DeleteRange(tc.min, tc.max); err != nil {
				t.Fatalf("DeleteRange(%d, %d): %v", tc.min, tc.max, err)
			}
			assertBounds(t, g, tc.wantFirst, tc.wantLast)
			for i := uint64(1); i <= 10; i++ {
				inRange := tc.min <= i && i <= tc.max
				if inRange {
					assertMissing(t, g, i)
				} else {
					assertLogs(t, g, logsRange(i, i))
				}
			}
		})
	}
}

// TestAppendAfterDeleteRange checks that the cached bounds drive the next
// write: after a suffix delete the log continues from the new last index,
// and after deleting everything (the snapshot-restore pattern) any index may
// start the log again.
func TestAppendAfterDeleteRange(t *testing.T) {
	t.Run("suffix then continue", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		mustStoreLogs(t, g, logsRange(1, 10))
		if err := g.DeleteRange(8, 10); err != nil {
			t.Fatalf("DeleteRange: %v", err)
		}
		if err := g.StoreLog(&raft.Log{Index: 8, Term: 9}); err != nil {
			t.Fatalf("StoreLog(8) after suffix delete: %v", err)
		}
		assertBounds(t, g, 1, 8)
	})
	t.Run("everything then any start", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		mustStoreLogs(t, g, logsRange(1, 3))
		if err := g.DeleteRange(0, math.MaxUint64); err != nil {
			t.Fatalf("DeleteRange: %v", err)
		}
		mustStoreLogs(t, g, logsRange(20, 22))
		assertBounds(t, g, 20, 22)
	})
}

// TestStoreLogsRejectsNonContiguousEntries checks the dense-index rule that
// backs IsMonotonic: appended entries continue the log, and a rejected call
// stores nothing.
func TestStoreLogsRejectsNonContiguousEntries(t *testing.T) {
	cases := []struct {
		name string
		in   []*raft.Log
	}{
		{"gap after last", []*raft.Log{{Index: 5, Term: 1}}},
		{"overwrite of last", []*raft.Log{{Index: 3, Term: 9}}},
		{"overwrite of first", []*raft.Log{{Index: 1, Term: 9}}},
		{"gap inside the call", []*raft.Log{{Index: 4, Term: 1}, {Index: 6, Term: 1}}},
		{"duplicate inside the call", []*raft.Log{{Index: 4, Term: 1}, {Index: 4, Term: 1}}},
		{"descending inside the call", []*raft.Log{{Index: 5, Term: 1}, {Index: 4, Term: 1}}},
		{"index zero", []*raft.Log{{Index: 0, Term: 1}}},
		{"nil entry", []*raft.Log{{Index: 4, Term: 1}, nil}},
		{"valid run after a gap", []*raft.Log{{Index: 7, Term: 1}, {Index: 4, Term: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestStore(t).Group(1)
			mustStoreLogs(t, g, logsRange(1, 3))
			if err := g.StoreLogs(tc.in); err == nil {
				t.Fatal("StoreLogs accepted an invalid run")
			}
			assertBounds(t, g, 1, 3)
			assertLogs(t, g, logsRange(1, 3))
			for _, l := range tc.in {
				if l != nil && l.Index > 3 {
					assertMissing(t, g, l.Index)
				}
			}
		})
	}
}

// TestStoreLogsStartsAnywhereOnEmptyGroup checks that an empty group takes
// any non-zero starting index, including the largest one.
func TestStoreLogsStartsAnywhereOnEmptyGroup(t *testing.T) {
	cases := []struct {
		name string
		lo   uint64
	}{
		{"index 7", 7},
		{"index 1 << 40", 1 << 40},
		{"maximum index", math.MaxUint64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestStore(t).Group(1)
			if err := g.StoreLog(&raft.Log{Index: tc.lo, Term: 1}); err != nil {
				t.Fatalf("StoreLog(%d) on empty group: %v", tc.lo, err)
			}
			assertBounds(t, g, tc.lo, tc.lo)
		})
	}
}

// TestStableStore checks the raft.StableStore contract for raw bytes and
// uint64 values.
func TestStableStore(t *testing.T) {
	t.Run("set then get", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		if err := g.Set([]byte("term"), []byte("value")); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, err := g.Get([]byte("term"))
		if err != nil || string(got) != "value" {
			t.Fatalf("Get: got %q, %v; want %q", got, err, "value")
		}
	})
	t.Run("overwrite", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		mustSet(t, g, []byte("k"), []byte("one"))
		mustSet(t, g, []byte("k"), []byte("two"))
		if got := mustGet(t, g, []byte("k")); string(got) != "two" {
			t.Fatalf("Get after overwrite: got %q", got)
		}
	})
	t.Run("empty key and empty value", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		mustSet(t, g, nil, []byte("nil-key"))
		mustSet(t, g, []byte{}, []byte("empty-key"))
		mustSet(t, g, []byte("empty-value"), nil)
		if got := mustGet(t, g, nil); string(got) != "empty-key" {
			t.Fatalf("Get(nil) = %q, want the value of the empty key", got)
		}
		if got := mustGet(t, g, []byte("empty-value")); len(got) != 0 {
			t.Fatalf("Get of empty value = %q, want empty", got)
		}
	})
	t.Run("binary keys", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		key := []byte{0x00, 'S', 0xff, 0x00}
		mustSet(t, g, key, []byte("binary"))
		if got := mustGet(t, g, key); string(got) != "binary" {
			t.Fatalf("Get(binary key) = %q", got)
		}
	})
	t.Run("missing key is empty without error", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		got, err := g.Get([]byte("absent"))
		if err != nil {
			t.Fatalf("Get(missing): %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("Get(missing) = %q, want empty", got)
		}
	})
	t.Run("set copies its arguments", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		key, val := []byte("key"), []byte("value")
		mustSet(t, g, key, val)
		key[0], val[0] = 'X', 'X'
		if got := mustGet(t, g, []byte("key")); string(got) != "value" {
			t.Fatalf("stored value changed with the caller buffer: %q", got)
		}
	})
	t.Run("get returns a copy", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		mustSet(t, g, []byte("key"), []byte("value"))
		got := mustGet(t, g, []byte("key"))
		got[0] = 'X'
		if again := mustGet(t, g, []byte("key")); string(again) != "value" {
			t.Fatalf("store changed through a returned slice: %q", again)
		}
	})
	t.Run("uint64 round trip", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		for _, v := range []uint64{0, 1, 1 << 40, math.MaxUint64} {
			if err := g.SetUint64([]byte("n"), v); err != nil {
				t.Fatalf("SetUint64(%d): %v", v, err)
			}
			got, err := g.GetUint64([]byte("n"))
			if err != nil || got != v {
				t.Fatalf("GetUint64: got %d, %v; want %d", got, err, v)
			}
		}
	})
	t.Run("uint64 is big-endian", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		if err := g.SetUint64([]byte("n"), 0x0102030405060708); err != nil {
			t.Fatalf("SetUint64: %v", err)
		}
		want := []byte{1, 2, 3, 4, 5, 6, 7, 8}
		if got := mustGet(t, g, []byte("n")); !bytes.Equal(got, want) {
			t.Fatalf("raw bytes = %v, want %v", got, want)
		}
	})
	t.Run("missing uint64 is zero without error", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		got, err := g.GetUint64([]byte("absent"))
		if err != nil || got != 0 {
			t.Fatalf("GetUint64(missing) = %d, %v; want 0, nil", got, err)
		}
	})
	t.Run("uint64 read of a non-8-byte value is an error", func(t *testing.T) {
		g := newTestStore(t).Group(1)
		mustSet(t, g, []byte("n"), []byte("abc"))
		if _, err := g.GetUint64([]byte("n")); err == nil {
			t.Fatal("GetUint64 accepted a 3 byte value")
		}
	})
}

func mustStoreLogs(t *testing.T, g *GroupStore, logs []*raft.Log) {
	t.Helper()
	if err := g.StoreLogs(logs); err != nil {
		t.Fatalf("StoreLogs: %v", err)
	}
}

func mustSet(t *testing.T, g *GroupStore, key, val []byte) {
	t.Helper()
	if err := g.Set(key, val); err != nil {
		t.Fatalf("Set(%q): %v", key, err)
	}
}

func mustGet(t *testing.T, g *GroupStore, key []byte) []byte {
	t.Helper()
	got, err := g.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return got
}

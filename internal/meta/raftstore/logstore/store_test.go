package logstore

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/hashicorp/raft"
)

// TestGroupsAreIsolated checks that two groups with overlapping log indices
// and identical stable keys keep their own values, including adjacent ids,
// and that deleting in one group leaves the other untouched.
func TestGroupsAreIsolated(t *testing.T) {
	s := newTestStore(t)
	g1, g2 := s.Group(1), s.Group(2)
	mustStoreLogs(t, g1, taggedLogs("g1", 1, 3))
	mustStoreLogs(t, g2, taggedLogs("g2", 1, 5))
	mustSet(t, g1, []byte("k"), []byte("g1-value"))
	mustSet(t, g2, []byte("k"), []byte("g2-value"))

	assertBounds(t, g1, 1, 3)
	assertBounds(t, g2, 1, 5)
	assertMissing(t, g1, 4)
	assertLogs(t, g1, taggedLogs("g1", 1, 3))
	assertLogs(t, g2, taggedLogs("g2", 1, 5))

	if got := mustGet(t, g1, []byte("k")); string(got) != "g1-value" {
		t.Fatalf("group 1 stable value = %q", got)
	}
	if got := mustGet(t, g2, []byte("k")); string(got) != "g2-value" {
		t.Fatalf("group 2 stable value = %q", got)
	}

	if err := g1.DeleteRange(1, 3); err != nil {
		t.Fatalf("DeleteRange: %v", err)
	}
	assertBounds(t, g1, 0, 0)
	assertBounds(t, g2, 1, 5)
	assertLogs(t, g2, taggedLogs("g2", 1, 5))
	if got := mustGet(t, g2, []byte("k")); string(got) != "g2-value" {
		t.Fatalf("group 2 stable value changed by group 1 delete: %q", got)
	}
}

// TestStableKeysCannotReachOtherGroups stores raw key bytes that spell out
// another group's prefix, and checks that they stay inside their own group.
func TestStableKeysCannotReachOtherGroups(t *testing.T) {
	s := newTestStore(t)
	g1, g2 := s.Group(1), s.Group(2)
	mustSet(t, g1, []byte{0, 0, 0, 2}, []byte("g1-looks-like-g2"))
	mustSet(t, g2, nil, []byte("g2-empty-key"))
	mustSet(t, g1, []byte{'L', 0, 0, 0, 0, 0, 0, 0, 1}, []byte("g1-looks-like-log"))

	if got := mustGet(t, g2, nil); string(got) != "g2-empty-key" {
		t.Fatalf("group 2 empty key = %q", got)
	}
	if got := mustGet(t, g1, []byte{0, 0, 0, 2}); string(got) != "g1-looks-like-g2" {
		t.Fatalf("group 1 prefix-like key = %q", got)
	}
	assertBounds(t, g1, 0, 0)
	assertBounds(t, g2, 0, 0)
	assertMissing(t, g1, 1)
}

// TestSameGroupIDReturnsSharedStore checks that Group returns the same store
// for the same id, so both handles see the same cached bounds.
func TestSameGroupIDReturnsSharedStore(t *testing.T) {
	s := newTestStore(t)
	a := s.Group(7)
	mustStoreLogs(t, a, logsRange(1, 4))
	b := s.Group(7)
	if a != b {
		t.Fatal("Group returned different stores for the same id")
	}
	assertBounds(t, b, 1, 4)
}

// TestReopenRestoresEverything writes logs and stable state for several
// groups, closes the store, reopens the directory and checks that every
// value, bound and gap comes back exactly.
func TestReopenRestoresEverything(t *testing.T) {
	dir := t.TempDir()
	cache := pebble.NewCache(16 << 20)
	t.Cleanup(cache.Unref)

	s, err := Open(dir, cache)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	g1 := s.Group(1)
	mustStoreLogs(t, g1, logsRange(1, 5))
	mustSet(t, g1, []byte("term"), []byte("t5"))
	if err := g1.SetUint64([]byte("vote"), 3); err != nil {
		t.Fatalf("SetUint64: %v", err)
	}
	g2 := s.Group(2)
	mustStoreLogs(t, g2, taggedLogs("g2", 10, 12))
	mustStoreLogs(t, s.Group(0), taggedLogs("g0", 1, 1))
	mustStoreLogs(t, s.Group(math.MaxUint32), taggedLogs("gmax", 1, 2))
	g3 := s.Group(3)
	mustSet(t, g3, []byte("only-stable"), []byte("x"))
	if err := g3.SetUint64([]byte("n"), 9); err != nil {
		t.Fatalf("SetUint64: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err = Open(dir, cache)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	g1 = s.Group(1)
	assertBounds(t, g1, 1, 5)
	assertLogs(t, g1, logsRange(1, 5))
	if got := mustGet(t, g1, []byte("term")); string(got) != "t5" {
		t.Fatalf("term after reopen = %q", got)
	}
	if v, err := g1.GetUint64([]byte("vote")); err != nil || v != 3 {
		t.Fatalf("vote after reopen = %d, %v", v, err)
	}

	g2 = s.Group(2)
	assertBounds(t, g2, 10, 12)
	assertLogs(t, g2, taggedLogs("g2", 10, 12))
	assertMissing(t, g2, 1)

	assertBounds(t, s.Group(0), 1, 1)
	assertBounds(t, s.Group(math.MaxUint32), 1, 2)

	g3 = s.Group(3)
	assertBounds(t, g3, 0, 0)
	assertMissing(t, g3, 1)
	if got := mustGet(t, g3, []byte("only-stable")); string(got) != "x" {
		t.Fatalf("stable value after reopen = %q", got)
	}
	if v, err := g3.GetUint64([]byte("n")); err != nil || v != 9 {
		t.Fatalf("uint64 after reopen = %d, %v", v, err)
	}
	assertBounds(t, s.Group(5), 0, 0)

	// Appends continue from the recovered last index.
	mustStoreLogs(t, g1, logsRange(6, 6))
	if err := g1.StoreLog(&raft.Log{Index: 8, Term: 1}); err == nil {
		t.Fatal("StoreLog accepted a gap after reopen")
	}
	assertBounds(t, g1, 1, 6)
}

// TestOpenRejectsMalformedKeys plants keys that no version of this store
// writes and checks that Open reports them, and that the directory lock is
// released so a later open of the same directory succeeds.
func TestOpenRejectsMalformedKeys(t *testing.T) {
	cases := []struct {
		name string
		key  []byte
	}{
		{"shorter than the group prefix", []byte{0, 0, 1}},
		{"unknown tag", []byte{0, 0, 0, 1, 'X', 0}},
		{"log key too short", []byte{0, 0, 0, 1, 'L', 0, 0}},
		{"log key too long", append(logKey(1, 1), 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			raw, err := pebble.Open(dir, &pebble.Options{})
			if err != nil {
				t.Fatalf("pebble.Open: %v", err)
			}
			if err := raw.Set(tc.key, []byte("junk"), pebble.Sync); err != nil {
				t.Fatalf("raw Set: %v", err)
			}
			if err := raw.Close(); err != nil {
				t.Fatalf("raw Close: %v", err)
			}

			s, err := Open(dir, nil)
			if err == nil {
				_ = s.Close()
				t.Fatal("Open accepted a malformed key")
			}

			again, err := pebble.Open(dir, &pebble.Options{})
			if err != nil {
				t.Fatalf("directory still locked after failed Open: %v", err)
			}
			if err := again.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

// TestOpenErrors checks the refusals that happen before any data is touched.
func TestOpenErrors(t *testing.T) {
	t.Run("empty directory name", func(t *testing.T) {
		if s, err := Open("", nil); err == nil {
			_ = s.Close()
			t.Fatal("Open(\"\") succeeded")
		}
	})
	t.Run("path is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("not a database"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if s, err := Open(file, nil); err == nil {
			_ = s.Close()
			t.Fatal("Open on a regular file succeeded")
		}
	})
}

// TestClosedStoreRefusesUse checks that every operation after Close returns
// ErrClosed, that nothing panics, and that a second Close returns nil.
func TestClosedStoreRefusesUse(t *testing.T) {
	s := newTestStore(t)
	g := s.Group(1)
	mustStoreLogs(t, g, logsRange(1, 2))
	mustSet(t, g, []byte("k"), []byte("v"))

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	var l raft.Log
	calls := map[string]func() error{
		"FirstIndex":  func() error { _, err := g.FirstIndex(); return err },
		"LastIndex":   func() error { _, err := g.LastIndex(); return err },
		"GetLog":      func() error { return g.GetLog(1, &l) },
		"StoreLog":    func() error { return g.StoreLog(&raft.Log{Index: 3, Term: 1}) },
		"StoreLogs":   func() error { return g.StoreLogs(logsRange(3, 4)) },
		"DeleteRange": func() error { return g.DeleteRange(1, 1) },
		"Set":         func() error { return g.Set([]byte("k"), []byte("v")) },
		"Get":         func() error { _, err := g.Get([]byte("k")); return err },
		"SetUint64":   func() error { return g.SetUint64([]byte("n"), 1) },
		"GetUint64":   func() error { _, err := g.GetUint64([]byte("n")); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrClosed) {
			t.Errorf("%s after Close: got %v, want ErrClosed", name, err)
		}
	}
	if err := s.Group(9).StoreLog(&raft.Log{Index: 1, Term: 1}); !errors.Is(err, ErrClosed) {
		t.Errorf("new group after Close: got %v, want ErrClosed", err)
	}
}

package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/ids"
)

const chunk = 1 << 20

func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	pool := bufpool.NewPool(chunk, bufpool.NewBudget(4*chunk), bufpool.WithAlignment(directAlign))
	s, err := NewStore(filepath.Join(root, "blobs"), filepath.Join(root, "tmp"), pool)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

func testID(n byte) ids.ShardID {
	var id ids.ShardID
	id.Data[0] = n
	id.Data[15] = 0xee
	id.Index = uint16(n)
	return id
}

func pattern(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*131) + seed + byte(i>>8)
	}
	return b
}

// writeAndPlace writes data as a blob for id and returns its final path.
func writeAndPlace(t *testing.T, s *Store, id ids.ShardID, data []byte) string {
	t.Helper()
	tmp, err := s.NewTemp()
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	if err := s.Write(context.Background(), tmp, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("write %d bytes: %v", len(data), err)
	}
	final := s.Path(id)
	if err := s.Place(tmp, final); err != nil {
		t.Fatalf("place: %v", err)
	}
	return final
}

func TestWriteExactSizes(t *testing.T) {
	sizes := []int{1, 4095, 4096, 4097, chunk - 1, chunk, chunk + 1, 3*chunk + 17}
	for i, n := range sizes {
		s := newTestStore(t)
		want := pattern(n, byte(i))
		final := writeAndPlace(t, s, testID(byte(i+1)), want)
		got, err := os.ReadFile(final)
		if err != nil {
			t.Fatalf("size %d: read back: %v", n, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("size %d: contents differ", n)
		}
	}
}

func TestWriteRejectsWrongInputAndCleansUp(t *testing.T) {
	boom := errors.New("network reset")
	cases := []struct {
		name string
		r    io.Reader
		size int64
	}{
		{"short", bytes.NewReader(pattern(chunk, 1)), chunk + 1},
		{"long", bytes.NewReader(pattern(chunk+1, 1)), chunk},
		{"small-long", bytes.NewReader(pattern(10, 1)), 9},
		{"reader-error", io.MultiReader(bytes.NewReader(pattern(5000, 1)), errReader{boom}), 9000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			tmp, err := s.NewTemp()
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Write(context.Background(), tmp, tc.r, tc.size); err == nil {
				t.Fatal("write accepted bad input")
			}
			if _, err := os.Stat(tmp); !os.IsNotExist(err) {
				t.Fatalf("temp file left behind: %v", err)
			}
		})
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestWriteHonoursCancellation(t *testing.T) {
	s := newTestStore(t)
	tmp, err := s.NewTemp()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = s.Write(ctx, tmp, bytes.NewReader(pattern(2*chunk, 2)), 2*chunk)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestPathLayoutAndNames(t *testing.T) {
	s := newTestStore(t)
	id := testID(0xab)
	name := ShardName(id)
	if len(name) != 2*ids.ShardIDSize {
		t.Fatalf("name %q has %d chars", name, len(name))
	}
	want := filepath.Join(s.blobs, name[:2], name)
	if got := s.Path(id); got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	back, err := ParseShardName(name)
	if err != nil || back != id {
		t.Fatalf("parse: %v %v", back, err)
	}
	for _, bad := range []string{"", "../../etc/passwd", name[:10], name + "0", "zz" + name[2:]} {
		if _, err := ParseShardName(bad); err == nil {
			t.Fatalf("ParseShardName(%q) accepted", bad)
		}
	}
}

func TestPlaceRemoveAndWalk(t *testing.T) {
	s := newTestStore(t)
	shards := []ids.ShardID{testID(1), testID(2), testID(3)}
	for i, id := range shards {
		writeAndPlace(t, s, id, pattern(100*(i+1), byte(i)))
	}

	seen := map[ids.ShardID]int64{}
	if err := s.Walk(func(id ids.ShardID, _ string, size int64) error {
		seen[id] = size
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(seen) != 3 || seen[shards[2]] != 300 {
		t.Fatalf("walk saw %v", seen)
	}

	if err := s.Remove(s.Path(shards[1])); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(s.Path(shards[1])); !os.IsNotExist(err) {
		t.Fatalf("blob still present: %v", err)
	}
	if err := s.Remove(s.Path(shards[1])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second remove: %v, want not-exist", err)
	}
}

func TestCleanTemp(t *testing.T) {
	s := newTestStore(t)
	tmp, err := s.NewTemp()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanTemp(); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temp file survived: %v", err)
	}
}

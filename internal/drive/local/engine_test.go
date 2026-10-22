package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// Small geometry for the engine tests: shards above 4 KiB become blobs, and a
// segment holds a few dozen packed records, so rolls and compaction happen
// quickly.
const (
	testPacked  = 4 << 10
	testSegment = 64 << 10
)

func testOptions() Options {
	return Options{
		ClusterID:          "test-cluster",
		PackedMaxShardSize: testPacked,
		SegmentSize:        testSegment,
		CompactRate:        -1,
	}
}

// newTestDir formats an empty drive directory.
func newTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := Format(context.Background(), dir, "test-cluster"); err != nil {
		t.Fatalf("format: %v", err)
	}
	return dir
}

// openAt opens the drive in dir and registers Close with t.
func openAt(t *testing.T, dir string, opts Options) *Drive {
	t.Helper()
	d, err := Open(context.Background(), dir, opts)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func idOf(n int) ids.ShardID {
	var id ids.ShardID
	id.Data[0] = byte(n >> 16)
	id.Data[1] = byte(n >> 8)
	id.Data[2] = byte(n)
	id.Data[15] = 0x77
	id.Index = uint16(n)
	return id
}

// payload returns n deterministic bytes that depend on seed.
func payload(n int, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*131 + seed*17 + i>>7)
	}
	return b
}

func writeShard(d *Drive, id ids.ShardID, data []byte) error {
	return d.WriteShard(context.Background(), id, bytes.NewReader(data), int64(len(data)))
}

func readShard(d *Drive, id ids.ShardID) ([]byte, error) {
	rc, err := d.ReadShard(context.Background(), id, 0, -1)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// snapshot is the set of visible shards and their contents.
type snapshot map[ids.ShardID][]byte

// capture reads every visible shard through the drive.
func capture(t *testing.T, d *Drive) snapshot {
	t.Helper()
	out := snapshot{}
	err := d.WalkShards(context.Background(), func(info drive.ShardInfo) error {
		data, err := readShard(d, info.ID)
		if err != nil {
			return err
		}
		out[info.ID] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

// requireSnapshot fails unless the drive shows exactly want.
func requireSnapshot(t *testing.T, d *Drive, want snapshot) {
	t.Helper()
	got := capture(t, d)
	if len(got) != len(want) {
		t.Fatalf("drive shows %d shards, want %d", len(got), len(want))
	}
	for id, data := range want {
		if !bytes.Equal(got[id], data) {
			t.Fatalf("shard %s: contents differ (%d bytes, want %d)", id, len(got[id]), len(data))
		}
	}
}

// closeDrive closes d and fails the test on error.
func closeDrive(t *testing.T, d *Drive) {
	t.Helper()
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// copyTree copies the regular files under src to dst. It stands in for the
// state a crash would leave behind when taken while the drive is running.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}

func TestReopenKeepsAcknowledgedShards(t *testing.T) {
	dir := newTestDir(t)
	want := snapshot{}
	d := openAt(t, dir, testOptions())
	for i := 0; i < 60; i++ {
		data := payload(100+i*97, i)
		if i%7 == 0 {
			data = payload(testPacked+1+i, i) // blob
		}
		if err := writeShard(d, idOf(i), data); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		want[idOf(i)] = data
	}
	for i := 0; i < 60; i += 5 {
		if err := d.DeleteShard(context.Background(), idOf(i)); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
		delete(want, idOf(i))
	}
	requireSnapshot(t, d, want)
	closeDrive(t, d)

	requireSnapshot(t, openAt(t, dir, testOptions()), want)
}

func TestSnapshotWhileRunningRecoversAcknowledgedWrites(t *testing.T) {
	dir := newTestDir(t)
	d := openAt(t, dir, testOptions())
	want := snapshot{}
	for i := 0; i < 30; i++ {
		data := payload(50*i+1, i)
		if err := writeShard(d, idOf(i), data); err != nil {
			t.Fatalf("write: %v", err)
		}
		want[idOf(i)] = data
	}
	if err := d.DeleteShard(context.Background(), idOf(3)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	delete(want, idOf(3))

	// The drive is still open: its files are copied as a crash would find them.
	crashed := filepath.Join(t.TempDir(), "crashed")
	copyTree(t, dir, crashed)
	requireSnapshot(t, openAt(t, crashed, testOptions()), want)
}

func TestRebuildFromSegmentsWhenIndexIsGone(t *testing.T) {
	dir := newTestDir(t)
	d := openAt(t, dir, testOptions())
	want := snapshot{}
	for i := 0; i < 40; i++ {
		data := payload(300+i, i)
		if i%9 == 0 {
			data = payload(2*testPacked, i)
		}
		if err := writeShard(d, idOf(i), data); err != nil {
			t.Fatalf("write: %v", err)
		}
		want[idOf(i)] = data
	}
	for _, i := range []int{1, 2, 9, 10} {
		if err := d.DeleteShard(context.Background(), idOf(i)); err != nil {
			t.Fatalf("delete: %v", err)
		}
		delete(want, idOf(i))
	}
	// Overwrite a deleted id: the tombstone must not hide the newer record.
	data := payload(77, 99)
	if err := writeShard(d, idOf(1), data); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	want[idOf(1)] = data
	closeDrive(t, d)

	if err := os.RemoveAll(filepath.Join(dir, ".unio", "index")); err != nil {
		t.Fatalf("remove index: %v", err)
	}
	requireSnapshot(t, openAt(t, dir, testOptions()), want)
}

func TestTornTailIsInvisibleAndTrimmed(t *testing.T) {
	for _, dropIndex := range []bool{false, true} {
		name := "trusted-index"
		if dropIndex {
			name = "rebuilt-index"
		}
		t.Run(name, func(t *testing.T) {
			dir := newTestDir(t)
			d := openAt(t, dir, testOptions())
			want := snapshot{}
			for i := 0; i < 5; i++ {
				data := payload(500, i)
				if err := writeShard(d, idOf(i), data); err != nil {
					t.Fatalf("write: %v", err)
				}
				want[idOf(i)] = data
			}
			end := d.committedEnd
			seg := segmentFile(t, d.lay, d.activeID)
			closeDrive(t, d)

			// A record that was being appended when the process died: its header
			// and part of its data reached the file, the rest did not.
			torn := segment.AppendRecord(nil, 0, idOf(100), payload(1000, 3))
			f, err := os.OpenFile(seg, os.O_WRONLY, 0)
			if err != nil {
				t.Fatalf("open segment: %v", err)
			}
			if _, err := f.WriteAt(torn[:len(torn)/2], end); err != nil {
				t.Fatalf("write torn record: %v", err)
			}
			f.Close()
			if dropIndex {
				if err := os.RemoveAll(filepath.Join(dir, ".unio", "index")); err != nil {
					t.Fatalf("remove index: %v", err)
				}
			}

			d2 := openAt(t, dir, testOptions())
			requireSnapshot(t, d2, want)
			if _, err := d2.StatShard(context.Background(), idOf(100)); !errors.Is(err, drive.ErrShardNotFound) {
				t.Fatalf("torn record is visible: %v", err)
			}
			if !dropIndex {
				info, err := os.Stat(seg)
				if err != nil || info.Size() != end {
					// The active segment is trimmed to its committed end on open.
					t.Fatalf("segment size %v (%v), want %d", info, err, end)
				}
			}
		})
	}
}

func TestOpenKeepsCorruptIndexAside(t *testing.T) {
	dir := newTestDir(t)
	closeDrive(t, openAt(t, dir, testOptions()))
	// An index path that cannot be opened as a database is set aside, not
	// deleted, and replaced by a rebuilt index.
	indexPath := filepath.Join(dir, ".unio", "index")
	if err := os.RemoveAll(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := openAt(t, dir, testOptions())
	matches, _ := filepath.Glob(indexPath + ".bad-*")
	if len(matches) != 1 {
		t.Fatalf("expected one set-aside index, found %v", matches)
	}
	requireSnapshot(t, d, snapshot{})
}

package local

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// FuzzRebuildFromSegment writes arbitrary bytes as the only segment of a drive
// whose index is missing, so that Open must rebuild from them. Opening may fail,
// but it must not panic. If it succeeds, every shard it lists must read back
// without a panic, and any read error must be a drive error.
func FuzzRebuildFromSegment(f *testing.F) {
	seed := testSegmentSeed(f)
	f.Add(seed)
	f.Add(seed[:len(seed)/2])
	f.Add([]byte{})
	// A header with a valid checksum that claims more data than the file holds,
	// and one whose length exceeds the largest length a header may carry.
	f.Add(append(segment.Header{ShardID: idOf(1), Length: 1 << 29}.AppendHeader(nil), seed[:100]...))
	f.Add(segment.Header{ShardID: idOf(2), Length: 1 << 63}.AppendHeader(nil))
	f.Fuzz(func(t *testing.T, content []byte) {
		if len(content) > testSegment {
			content = content[:testSegment]
		}
		dir := newTestDir(t)
		segPath := newLayout(dir).segmentPath(1)
		if err := os.WriteFile(segPath, content, 0o600); err != nil {
			t.Fatalf("write segment: %v", err)
		}
		if err := os.RemoveAll(filepath.Join(dir, ".unio", "index")); err != nil {
			t.Fatalf("remove index: %v", err)
		}
		d, err := Open(context.Background(), dir, testOptions())
		if err != nil {
			return
		}
		defer d.Close()
		err = d.WalkShards(context.Background(), func(info drive.ShardInfo) error {
			rc, err := d.ReadShard(context.Background(), info.ID, 0, -1)
			if err != nil {
				return err
			}
			defer rc.Close()
			_, err = io.Copy(io.Discard, rc)
			return err
		})
		if err != nil && !errors.Is(err, drive.ErrCorrupt) && !errors.Is(err, drive.ErrShardNotFound) {
			t.Fatalf("unexpected error reading rebuilt drive: %v", err)
		}
	})
}

// testSegmentSeed returns the bytes of a small valid segment with a live
// record and a tombstone, used as a fuzzing seed.
func testSegmentSeed(f *testing.F) []byte {
	f.Helper()
	dir := f.TempDir()
	if _, err := Format(context.Background(), dir, "test-cluster"); err != nil {
		f.Fatalf("format: %v", err)
	}
	d, err := Open(context.Background(), dir, testOptions())
	if err != nil {
		f.Fatalf("open: %v", err)
	}
	if err := writeShard(d, idOf(1), payload(100, 1)); err != nil {
		f.Fatalf("write: %v", err)
	}
	if err := writeShard(d, idOf(2), payload(200, 2)); err != nil {
		f.Fatalf("write: %v", err)
	}
	if err := d.DeleteShard(context.Background(), idOf(1)); err != nil {
		f.Fatalf("delete: %v", err)
	}
	if err := d.Close(); err != nil {
		f.Fatalf("close: %v", err)
	}
	data, err := os.ReadFile(newLayout(dir).segmentPath(1))
	if err != nil {
		f.Fatalf("read segment: %v", err)
	}
	return data[:int(d.committedEnd)]
}

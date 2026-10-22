package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/pebble/v2"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// setRawIndexEntry overwrites the index entry of id in the closed drive at dir.
// A running drive owns its index, so Pebble is opened directly. The layout
// mirrors index.Location: kind, then segment, offset, length and creation time
// as big-endian uint64 values.
func setRawIndexEntry(t *testing.T, dir string, id ids.ShardID, kind byte, seg, off, length, created uint64) {
	t.Helper()
	db, err := pebble.Open(filepath.Join(dir, ".unio", "index"), &pebble.Options{})
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer db.Close()
	key := append([]byte{0x01}, id.AppendBinary(nil)...) // 0x01: shard keys
	value := []byte{kind}
	for _, v := range []uint64{seg, off, length, created} {
		value = append(value, byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
	if err := db.Set(key, value, pebble.Sync); err != nil {
		t.Fatalf("write raw entry: %v", err)
	}
}

// TestOpenRebuildsWhenIndexEntryIsCorrupt damages one index entry in ways that
// can come from bitrot. The index must be found inconsistent, so that Open
// rebuilds it from the segments, and every shard must come back intact.
func TestOpenRebuildsWhenIndexEntryIsCorrupt(t *testing.T) {
	cases := []struct {
		name   string
		kind   byte
		seg    uint64
		off    uint64
		length uint64
	}{
		{"offset-beyond-int64", byte(index.KindPacked), 1, 1 << 63, 1000},
		{"length-beyond-int64", byte(index.KindPacked), 1, 0, 1 << 63},
		{"unknown-segment", byte(index.KindPacked), 99, 0, 1000},
		{"beyond-committed-end", byte(index.KindPacked), 1, 1 << 40, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newTestDir(t)
			opts := testOptions()
			d := openAt(t, dir, opts)
			want := snapshot{}
			for i := 1; i <= 5; i++ {
				data := payload(1000, i)
				if err := writeShard(d, idOf(i), data); err != nil {
					t.Fatalf("write: %v", err)
				}
				want[idOf(i)] = data
			}
			closeDrive(t, d)

			setRawIndexEntry(t, dir, idOf(3), tc.kind, tc.seg, tc.off, tc.length, 1)
			d2 := openAt(t, dir, opts)
			if !d2.Recovery().Rebuilt {
				t.Fatal("corrupt index entry did not trigger a rebuild")
			}
			requireSnapshot(t, d2, want)
		})
	}
}

// TestReadReportsSegmentMissingFromTheDriveAsCorrupt simulates an index entry
// that names a segment the drive no longer has. The read must fail with
// ErrCorrupt, not with a retry or a generic error.
func TestReadReportsSegmentMissingFromTheDriveAsCorrupt(t *testing.T) {
	d := openAt(t, newTestDir(t), testOptions())
	id := idOf(1)
	if err := writeShard(d, id, payload(1000, 1)); err != nil {
		t.Fatalf("write: %v", err)
	}
	loc, err := d.idx.Get(id)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	d.stats.remove(loc.Segment)
	if err := d.segs.retire(loc.Segment); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := readShard(d, id); !errors.Is(err, drive.ErrCorrupt) {
		t.Fatalf("read of a shard whose segment is missing: err = %v, want ErrCorrupt", err)
	}
}

// TestRecordHeaderWithForgedLengthIsCorrupt rewrites a stored record header with
// a valid checksum but a length the index does not agree with. Full and range
// reads must both report ErrCorrupt.
func TestRecordHeaderWithForgedLengthIsCorrupt(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	d := openAt(t, dir, opts)
	id := idOf(1)
	data := payload(1000, 1)
	if err := writeShard(d, id, data); err != nil {
		t.Fatalf("write: %v", err)
	}
	loc, err := d.idx.Get(id)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	closeDrive(t, d)

	forged := segment.Header{ShardID: id, Length: 1 << 29, DataCRC: segment.Checksum(data)}.AppendHeader(nil)
	raw, err := os.OpenFile(newLayout(dir).segmentPath(loc.Segment), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	if _, err := raw.WriteAt(forged, loc.Offset); err != nil {
		t.Fatalf("forge header: %v", err)
	}
	raw.Close()

	d2 := openAt(t, dir, opts)
	if _, err := readShard(d2, id); !errors.Is(err, drive.ErrCorrupt) {
		t.Fatalf("full read with forged header: err = %v, want ErrCorrupt", err)
	}
	rc, err := d2.ReadShard(context.Background(), id, 0, 10)
	if err == nil {
		rc.Close()
	}
	if !errors.Is(err, drive.ErrCorrupt) {
		t.Fatalf("range read with forged header: err = %v, want ErrCorrupt", err)
	}
}

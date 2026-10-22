package local

import (
	"os"
	"testing"

	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// TestRebuildKeepsRecordsAroundMidSegmentDamage damages one record in the middle
// of a sealed segment, then rebuilds the index. Every other record, including
// the ones after the damage in the same segment, must come back intact.
func TestRebuildKeepsRecordsAroundMidSegmentDamage(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	d := openAt(t, dir, opts)
	want := snapshot{}
	// 1000-byte records of 1042 bytes: 62 fit in a 64 KiB segment, so segments 1
	// and 2 are sealed and segment 3 is active.
	for i := 1; i <= 120; i++ {
		data := payload(1000, i)
		if err := writeShard(d, idOf(i), data); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		want[idOf(i)] = data
	}
	closeDrive(t, d)

	// Destroy the header of record 31 (index 30) in sealed segment 1.
	const recordSize = segment.HeaderSize + 1000
	raw, err := os.OpenFile(segmentFile(t, layoutOf(t, dir), 1), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	if _, err := raw.WriteAt([]byte{0}, 30*recordSize); err != nil {
		t.Fatalf("damage: %v", err)
	}
	raw.Close()
	delete(want, idOf(31))

	if err := removeIndex(dir); err != nil {
		t.Fatal(err)
	}
	requireSnapshot(t, openAt(t, dir, opts), want)
}

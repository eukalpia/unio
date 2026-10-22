package local

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// failAt returns hooks that fail the first n checks at point p.
func failAt(p faults.Point, n int64) *faults.Hooks {
	var left atomic.Int64
	left.Store(n)
	return &faults.Hooks{Fail: func(q faults.Point) error {
		if q == p && left.Add(-1) >= 0 {
			return errInjected
		}
		return nil
	}}
}

var errInjected = errors.New("injected fault")

// TestFailedCommitIsInvisibleAndDriveRecovers injects a failure at each point
// where a write can fail after its bytes reached the segment. The write must
// report an error, must not be visible now or after a restart, and must not
// stop later writes.
func TestFailedCommitIsInvisibleAndDriveRecovers(t *testing.T) {
	// A failed fsync is not recoverable: it makes the drive faulty (see
	// fsync_test.go). Only a failure before anything reaches the disk is.
	cases := []struct {
		name  string
		point faults.Point
	}{
		{"index-commit", faults.IndexCommit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newTestDir(t)
			opts := testOptions()
			d := openAt(t, dir, opts)
			ctx := context.Background()
			want := snapshot{}

			good := payload(300, 1)
			if err := writeShard(d, idOf(1), good); err != nil {
				t.Fatalf("first write: %v", err)
			}
			want[idOf(1)] = good

			d.opts.hooks = failAt(tc.point, 1)
			if err := writeShard(d, idOf(2), payload(300, 2)); !errors.Is(err, errInjected) {
				t.Fatalf("injected write: err = %v", err)
			}
			if _, err := d.StatShard(ctx, idOf(2)); !errors.Is(err, drive.ErrShardNotFound) {
				t.Fatalf("failed write is visible: %v", err)
			}

			after := payload(400, 3)
			if err := writeShard(d, idOf(3), after); err != nil {
				t.Fatalf("write after failure: %v", err)
			}
			want[idOf(3)] = after
			requireSnapshot(t, d, want)
			closeDrive(t, d)

			requireSnapshot(t, openAt(t, dir, opts), want)
		})
	}
}

// TestFailedDeleteKeepsShard covers a delete whose tombstone was appended but
// whose index commit failed: the shard must stay readable.
func TestFailedDeleteKeepsShard(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	d := openAt(t, dir, opts)
	data := payload(200, 9)
	if err := writeShard(d, idOf(1), data); err != nil {
		t.Fatalf("write: %v", err)
	}
	d.opts.hooks = failAt(faults.IndexCommit, 1)
	if err := d.DeleteShard(context.Background(), idOf(1)); !errors.Is(err, errInjected) {
		t.Fatalf("injected delete: err = %v", err)
	}
	if got, err := readShard(d, idOf(1)); err != nil || string(got) != string(data) {
		t.Fatalf("shard lost after failed delete: %v", err)
	}
	closeDrive(t, d)
	if got, err := readShard(openAt(t, dir, opts), idOf(1)); err != nil || string(got) != string(data) {
		t.Fatalf("shard lost after restart: %v", err)
	}
}

// TestFailedBlobPlacementLeavesNoFile checks that a blob whose index commit
// failed is removed from the blob directory.
func TestFailedBlobPlacementLeavesNoFile(t *testing.T) {
	dir := newTestDir(t)
	d := openAt(t, dir, testOptions())
	d.opts.hooks = failAt(faults.IndexCommit, 1)
	big := payload(2*testPacked, 4)
	if err := writeShard(d, idOf(7), big); !errors.Is(err, errInjected) {
		t.Fatalf("injected blob write: err = %v", err)
	}
	if _, err := d.StatShard(context.Background(), idOf(7)); !errors.Is(err, drive.ErrShardNotFound) {
		t.Fatalf("failed blob is visible: %v", err)
	}
	if _, err := os.Stat(blobFile(t, d.blobs, idOf(7))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blob file left behind: %v", err)
	}
}

// TestFullReadDetectsBitFlip flips a bit as it is read from disk. A whole-shard
// read must fail with ErrCorrupt once it reaches the end.
func TestFullReadDetectsBitFlip(t *testing.T) {
	dir := newTestDir(t)
	d := openAt(t, dir, testOptions())
	if err := writeShard(d, idOf(1), payload(500, 5)); err != nil {
		t.Fatalf("write: %v", err)
	}
	d.opts.hooks = &faults.Hooks{Mutate: func(p faults.Point, b []byte) {
		if p == faults.ReadData && len(b) > 0 {
			b[0] ^= 0x01
		}
	}}
	_, err := readShard(d, idOf(1))
	if !errors.Is(err, drive.ErrCorrupt) {
		t.Fatalf("full read with flipped bit: err = %v, want ErrCorrupt", err)
	}
}

// TestOnDiskCorruptionIsDetected damages a stored record and checks that a
// whole-shard read reports ErrCorrupt rather than wrong bytes.
func TestOnDiskCorruptionIsDetected(t *testing.T) {
	dir := newTestDir(t)
	d := openAt(t, dir, testOptions())
	data := payload(500, 6)
	if err := writeShard(d, idOf(1), data); err != nil {
		t.Fatalf("write: %v", err)
	}
	loc, err := d.idx.Get(idOf(1))
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	closeDrive(t, d)

	seg := segmentFile(t, d.lay, loc.Segment)
	raw, err := os.OpenFile(seg, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	if _, err := raw.WriteAt([]byte{^data[100]}, loc.Offset+segment.HeaderSize+100); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	raw.Close()

	d2 := openAt(t, dir, testOptions())
	if _, err := readShard(d2, idOf(1)); !errors.Is(err, drive.ErrCorrupt) {
		t.Fatalf("read of damaged shard: err = %v, want ErrCorrupt", err)
	}
	// A range that avoids the damage still reads, and the header check passes.
	rc, err := d2.ReadShard(context.Background(), idOf(1), 0, 50)
	if err != nil {
		t.Fatalf("range read: %v", err)
	}
	rc.Close()
}

// TestGroupCommitSharesSyncs runs many small concurrent writers against a
// device that takes time to sync. Writers queued during one sync must share the
// next one, so the number of syncs stays well below the number of writes.
func TestGroupCommitSharesSyncs(t *testing.T) {
	dir := newTestDir(t)
	d := openAt(t, dir, testOptions())
	var syncs atomic.Int64
	d.opts.hooks = &faults.Hooks{Fail: func(p faults.Point) error {
		if p == faults.SegmentSync {
			syncs.Add(1)
			time.Sleep(2 * time.Millisecond) // models device latency, not synchronisation
		}
		return nil
	}}

	const writers = 64
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- writeShard(d, idOf(1000+i), payload(4096, i))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
	n := syncs.Load()
	if n >= writers/2 {
		t.Fatalf("%d syncs for %d writers: group commit is not sharing syncs", n, writers)
	}
	t.Logf("%d writers shared %d segment syncs", writers, n)
}

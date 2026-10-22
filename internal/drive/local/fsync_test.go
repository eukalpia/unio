package local

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// syncFailer fails every call at one injection point and counts the calls.
// After a failed fsync the engine must never call it again, so the count is
// how the tests check for retries.
type syncFailer struct {
	point faults.Point
	fail  atomic.Bool
	calls atomic.Int64
}

func (s *syncFailer) hooks() *faults.Hooks {
	return &faults.Hooks{Fail: func(p faults.Point) error {
		if p != s.point {
			return nil
		}
		s.calls.Add(1)
		if s.fail.Load() {
			return errInjected
		}
		return nil
	}}
}

func requireFaulty(t *testing.T, err error, what string) {
	t.Helper()
	if !errors.Is(err, drive.ErrFaulty) {
		t.Fatalf("%s: err = %v, want drive.ErrFaulty", what, err)
	}
}

// TestFailedFsyncFailsEveryWriteInTheGroup puts three writes into one group
// commit and fails the fsync that covers them. All three must fail, none may be
// visible, the drive must refuse further writes, and a restart must show only
// the write acknowledged before the failure.
func TestFailedFsyncFailsEveryWriteInTheGroup(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	d := openAt(t, dir, opts)
	ctx := context.Background()
	acked := payload(300, 1)
	if err := writeShard(d, idOf(1), acked); err != nil {
		t.Fatalf("first write: %v", err)
	}

	sf := &syncFailer{point: faults.SegmentSync}
	d.opts.hooks = sf.hooks()
	sf.fail.Store(true)

	g := d.newGroup()
	var replies []*request
	for i := 2; i <= 4; i++ {
		r := newRequest(reqPut, idOf(i))
		r.rec = segment.AppendRecord(nil, 0, idOf(i), payload(300, i))
		r.created = time.Now().UnixNano()
		replies = append(replies, r)
		d.handle(g, r)
	}
	requireFaulty(t, g.flush(), "group commit")
	for i, r := range replies {
		requireFaulty(t, <-r.reply, "write in the failed group "+string(rune('2'+i)))
	}
	if n := sf.calls.Load(); n != 1 {
		t.Fatalf("fsync attempted %d times for one group, want 1", n)
	}
	for i := 2; i <= 4; i++ {
		if _, err := d.StatShard(ctx, idOf(i)); !errors.Is(err, drive.ErrShardNotFound) {
			t.Fatalf("write %d of the failed group is visible: %v", i, err)
		}
	}

	requireFaulty(t, writeShard(d, idOf(9), payload(10, 9)), "write after the fault")
	requireFaulty(t, d.DeleteShard(ctx, idOf(1)), "delete after the fault")
	if got, err := readShard(d, idOf(1)); err != nil || !bytes.Equal(got, acked) {
		t.Fatalf("reads must continue after a fault: %v", err)
	}
	info, err := d.Info(ctx)
	if err != nil || info.Online {
		t.Fatalf("info after fault: online=%v err=%v; want offline", info.Online, err)
	}
	requireFaulty(t, d.Close(), "close of a faulty drive")
	if n := sf.calls.Load(); n != 1 {
		t.Fatalf("fsync retried during close: %d calls", n)
	}

	requireSnapshot(t, openAt(t, dir, testOptions()), snapshot{idOf(1): acked})
}

// TestFailedIndexSyncPoisonsDrive covers the index's write-ahead log sync.
func TestFailedIndexSyncPoisonsDrive(t *testing.T) {
	d := openAt(t, newTestDir(t), testOptions())
	sf := &syncFailer{point: faults.IndexSync}
	d.opts.hooks = sf.hooks()
	sf.fail.Store(true)

	requireFaulty(t, writeShard(d, idOf(1), payload(100, 1)), "write with a failed index sync")
	requireFaulty(t, writeShard(d, idOf(2), payload(100, 2)), "next write")
	if n := sf.calls.Load(); n != 1 {
		t.Fatalf("index sync retried: %d calls", n)
	}
}

// TestFailedDirectorySyncAtRollPoisonsDrive fails the directory sync that makes
// a new segment durable. The write that needed the segment must fail, and the
// drive must then refuse writes but keep serving the data it has.
func TestFailedDirectorySyncAtRollPoisonsDrive(t *testing.T) {
	d := openAt(t, newTestDir(t), testOptions())
	sf := &syncFailer{point: faults.DirSync}
	d.opts.hooks = sf.hooks()
	sf.fail.Store(true)

	failedAt := 0
	for i := 1; i <= 200; i++ {
		if err := writeShard(d, idOf(i), payload(1000, i)); err != nil {
			requireFaulty(t, err, "write that needed a new segment")
			failedAt = i
			break
		}
	}
	if failedAt == 0 {
		t.Fatal("no write failed at a roll: the directory sync was not reached")
	}
	requireFaulty(t, writeShard(d, idOf(999), payload(10, 9)), "write after the fault")
	if _, err := readShard(d, idOf(1)); err != nil {
		t.Fatalf("read after the fault: %v", err)
	}
}

// TestFailedFsyncInBatchModePoisonsDriveAndIsNotRetried uses SyncBatch. A write
// is acknowledged before the fsync that makes it durable, so the failure is
// found by the next sync. The drive must become faulty at that point, and no
// later operation may call fsync again.
func TestFailedFsyncInBatchModePoisonsDriveAndIsNotRetried(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	opts.SyncPolicy = SyncBatch
	opts.BatchInterval = time.Hour // no background sync during the test
	d := openAt(t, dir, opts)
	ctx := context.Background()
	acked := payload(300, 1)
	if err := writeShard(d, idOf(1), acked); err != nil {
		t.Fatalf("first write: %v", err)
	}

	sf := &syncFailer{point: faults.SegmentSync}
	d.opts.hooks = sf.hooks()
	sf.fail.Store(true)
	if err := writeShard(d, idOf(2), payload(300, 2)); err != nil {
		t.Fatalf("batch-mode write before the sync: %v", err)
	}
	requireFaulty(t, d.syncIfDirty(), "sync of batched writes")

	requireFaulty(t, writeShard(d, idOf(3), payload(10, 3)), "write after the failed sync")
	requireFaulty(t, d.DeleteShard(ctx, idOf(1)), "delete after the failed sync")
	if got, err := readShard(d, idOf(1)); err != nil || !bytes.Equal(got, acked) {
		t.Fatalf("reads must continue after a fault: %v", err)
	}
	requireFaulty(t, d.Close(), "close of a faulty drive")
	if n := sf.calls.Load(); n != 1 {
		t.Fatalf("fsync attempted %d times, want 1", n)
	}
}

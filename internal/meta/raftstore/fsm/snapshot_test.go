package fsm

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/hashicorp/raft"

	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// memSink is an in-memory raft.SnapshotSink.
type memSink struct {
	bytes.Buffer
	closed    bool
	cancelled bool
}

var _ raft.SnapshotSink = (*memSink)(nil)

func (s *memSink) ID() string    { return "test-snapshot" }
func (s *memSink) Cancel() error { s.cancelled = true; return nil }
func (s *memSink) Close() error  { s.closed = true; return nil }

// persistSnapshot captures the current state and streams it.
func persistSnapshot(t *testing.T, f *FSM) []byte {
	t.Helper()
	snap, err := f.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer snap.Release()
	return persistTo(t, snap)
}

// persistTo streams a captured snapshot into memory.
func persistTo(t *testing.T, snap raft.FSMSnapshot) []byte {
	t.Helper()
	sink := &memSink{}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if !sink.closed {
		t.Fatal("persist did not close the sink")
	}
	return sink.Bytes()
}

// buildDataState writes a mix of everything a data partition holds: versions,
// delete markers, uploads with parts, and control records.
func buildDataState(h *harness) {
	h.mustApply(versioningCmd(testBucket, enabled))
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("dir%d/obj%02d", i%3, i)
		h.mustApply(putCmd(testBucket, objRec(key, fmt.Sprintf("v%d", i), fmt.Sprintf("e%d", i)), nil))
		if i%4 == 0 {
			h.mustApply(deleteCmd(testBucket, key, "", fmt.Sprintf("m%d", i), nil))
		}
	}
	h.mustApply(createUploadCmd(testBucket, "dir1/up", "u1"))
	h.mustApply(putPartCmd(testBucket, "dir1/up", "u1", 1, "p1", partData(1)))
	h.mustApply(putPartCmd(testBucket, "dir1/up", "u1", 2, "p2", partData(2)))
	h.mustApply(createUploadCmd(otherBucket, "other", "u9"))
	h.mustApply(putCmd(otherBucket, objRec("other", "", etagA), nil))
}

// restoreFrom loads data into f, which must be a fresh or any other FSM.
func restoreFrom(t *testing.T, f *FSM, data []byte) {
	t.Helper()
	if err := f.Restore(io.NopCloser(bytes.NewReader(data))); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

// applyBoth applies one command at the next index to both harnesses and
// checks that the proposer-visible results agree.
func applyBoth(t *testing.T, a, b *harness, cmd *pb.Command) {
	t.Helper()
	a.index++
	b.index = a.index
	oa := apply(t, a.f, a.index, cmd)
	ob := apply(t, b.f, b.index, cmd)
	if (oa.Err == nil) != (ob.Err == nil) {
		t.Fatalf("index %d: errors differ: %v vs %v", a.index, oa.Err, ob.Err)
	}
	if oa.Err != nil && s3err.CodeOf(oa.Err) != s3err.CodeOf(ob.Err) {
		t.Fatalf("index %d: codes differ: %v vs %v", a.index, oa.Err, ob.Err)
	}
	if oa.Result != nil && ob.Result != nil {
		ra, rb := oa.Result, ob.Result
		if ra.VersionId != rb.VersionId || ra.DeleteMarker != rb.DeleteMarker ||
			len(ra.Garbage) != len(rb.Garbage) || len(ra.UnusedParts) != len(rb.UnusedParts) {
			t.Fatalf("index %d: results differ: %+v vs %+v", a.index, ra, rb)
		}
	}
}

func TestSnapshotRoundTripIntoFreshFSM(t *testing.T) {
	src := newHarness(t, KindData)
	buildDataState(src)
	data := persistSnapshot(t, src.f)

	// The destination already holds unrelated state, which restore replaces.
	dst := newHarness(t, KindData)
	dst.mustApply(putCmd(testBucket, objRec("junk", "", etagA), nil))
	dst.mustApply(createUploadCmd(testBucket, "junk", "junk-upload"))

	restoreFrom(t, dst.f, data)
	if dst.f.Applied() != src.f.Applied() {
		t.Fatalf("applied after restore = %d, want %d", dst.f.Applied(), src.f.Applied())
	}
	if !equalState(dst.dumpState(), src.dumpState()) {
		t.Fatal("restored state differs from the source")
	}
	dst.index = src.index
	if _, err := dst.getObject("junk", ""); s3err.CodeOf(err) != s3err.NoSuchKey {
		t.Fatalf("state from before the restore survived: %v", err)
	}

	// Both machines keep agreeing on everything that follows, including the
	// version counter and the upload and part state.
	for i := 0; i < 20; i++ {
		applyBoth(t, src, dst, putCmd(testBucket, objRec("after", fmt.Sprintf("n%d", i), "e"), nil))
	}
	applyBoth(t, src, dst, deleteCmd(testBucket, "after", "", "mz", nil))
	applyBoth(t, src, dst, &pb.Command{Cmd: &pb.Command_CompleteUpload{CompleteUpload: &pb.CompleteUpload{
		BucketId: uint64(testBucket),
		Key:      "dir1/up",
		UploadId: "u1",
		Record: &pb.ObjectRecord{
			Bucket:    "bkt",
			Key:       "dir1/up",
			VersionId: "vup",
			Parts:     []*pb.PartRecord{listedPart(1, partData(1))},
		},
	}}})
	if !equalState(dst.dumpState(), src.dumpState()) {
		t.Fatal("states diverged after the restore")
	}
	if dst.f.Applied() != src.f.Applied() {
		t.Fatalf("applied diverged: %d vs %d", dst.f.Applied(), src.f.Applied())
	}
}

// A snapshot is a point-in-time view: commands applied after it is taken must
// not appear in its stream.
func TestSnapshotIsPointInTime(t *testing.T) {
	src := newHarness(t, KindData)
	buildDataState(src)
	atSnapshot := src.f.Applied()

	snap, err := src.f.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	src.mustApply(putCmd(testBucket, objRec("written-after-snapshot", "vz", "ez"), nil))
	data := persistTo(t, snap)
	snap.Release()

	dst := newHarness(t, KindData)
	restoreFrom(t, dst.f, data)
	if dst.f.Applied() != atSnapshot {
		t.Fatalf("applied in snapshot = %d, want %d", dst.f.Applied(), atSnapshot)
	}
	if _, err := dst.getObject("written-after-snapshot", ""); s3err.CodeOf(err) != s3err.NoSuchKey {
		t.Fatalf("key written after the snapshot leaked into it: %v", err)
	}
}

func TestSnapshotRoundTripSystemGroup(t *testing.T) {
	src := newHarness(t, KindSystem)
	src.mustApply(createBucketCmd("alpha", "alice"))
	b := src.mustApply(createBucketCmd("bravo", "bob")).Bucket
	src.mustApply(markDeletingCmd("bravo", b.Id, true))
	src.mustApply(kvPutCmd("cluster/x", []byte("1"), 0))
	src.mustApply(kvPutCmd("cluster/y", []byte("2"), 0))
	data := persistSnapshot(t, src.f)

	dst := newHarness(t, KindSystem)
	restoreFrom(t, dst.f, data)
	if !equalState(dst.dumpState(), src.dumpState()) {
		t.Fatal("system state differs after restore")
	}
	if got := len(dst.listBuckets()); got != 1 {
		t.Fatalf("visible buckets after restore = %d, want 1", got)
	}
	// The bucket id counter came across, so the next id continues the sequence.
	dst.index = src.index
	applyBoth(t, src, dst, createBucketCmd("charlie", "carol"))
	c, err := dst.getBucket("charlie", false)
	if err != nil || c.Id != 3 {
		t.Fatalf("bucket created after restore = %v, %v; want id 3", c, err)
	}
}

// A stream that is not a snapshot is rejected before anything is cleared, so
// the state is untouched and the FSM keeps working.
func TestRestoreRejectsBadStreamWithoutFailing(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))
	before := h.dumpState()

	err := h.f.Restore(io.NopCloser(strings.NewReader("not a snapshot")))
	if err == nil {
		t.Fatal("bad stream: want error")
	}
	h.assertUnchanged(before)

	h.mustApply(putCmd(testBucket, objRec("j", "", etagB), nil))
	if _, err := h.getObject("j", ""); err != nil {
		t.Fatalf("FSM unusable after a rejected stream: %v", err)
	}
}

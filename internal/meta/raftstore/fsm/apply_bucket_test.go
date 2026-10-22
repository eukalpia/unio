package fsm

import (
	"testing"

	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

func createBucketCmd(name, owner string) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{
		Name:            name,
		Owner:           owner,
		CreatedUnixNano: 1,
	}}}
}

func markDeletingCmd(name string, id uint64, deleting bool) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_MarkBucketDeleting{MarkBucketDeleting: &pb.MarkBucketDeleting{
		Name:     name,
		BucketId: id,
		Deleting: deleting,
	}}}
}

func deleteBucketCmd(name string, id uint64) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_DeleteBucket{DeleteBucket: &pb.DeleteBucket{Name: name, BucketId: id}}}
}

func setBucketVersioningCmd(name string, id uint64, state pb.VersioningState) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_SetBucketVersioning{SetBucketVersioning: &pb.SetBucketVersioning{
		Name:       name,
		BucketId:   id,
		Versioning: state,
	}}}
}

// getBucket reads a bucket by name.
func (h *harness) getBucket(name string, includeDeleting bool) (*pb.BucketRecord, error) {
	h.t.Helper()
	res, err := h.read(&pb.ReadQuery{Query: &pb.ReadQuery_GetBucket{GetBucket: &pb.GetBucketQuery{
		Name:            name,
		IncludeDeleting: includeDeleting,
	}}})
	if err != nil {
		return nil, err
	}
	return res.GetBucket().GetBucket(), nil
}

// listBuckets returns every visible bucket record.
func (h *harness) listBuckets() []*pb.BucketRecord {
	h.t.Helper()
	res, err := h.read(&pb.ReadQuery{Query: &pb.ReadQuery_ListBuckets{ListBuckets: &pb.ListBucketsQuery{}}})
	if err != nil {
		h.t.Fatalf("list buckets: %v", err)
	}
	return res.GetBuckets().GetBuckets()
}

func TestCreateBucket(t *testing.T) {
	h := newHarness(t, KindSystem)

	a := h.mustApply(createBucketCmd("a", "alice")).Bucket
	if a == nil || a.Id != 1 || a.Name != "a" || a.Owner != "alice" {
		t.Fatalf("first bucket = %v, want id 1", a)
	}
	b := h.mustApply(createBucketCmd("b", "bob")).Bucket
	if b.Id != 2 {
		t.Fatalf("second bucket id = %d, want 2", b.Id)
	}

	before := h.dumpState()
	wantCode(t, h.mustReject(createBucketCmd("a", "alice")), s3err.BucketAlreadyOwnedByYou)
	h.assertUnchanged(before)
	wantCode(t, h.mustReject(createBucketCmd("a", "mallory")), s3err.BucketAlreadyExists)
	h.assertUnchanged(before)

	h.mustReject(createBucketCmd("", "alice"))

	// Ids come from a counter that never goes back, so a deleted bucket's id
	// is not reused by a bucket created under the same name.
	h.mustApply(markDeletingCmd("a", a.Id, true))
	h.mustApply(deleteBucketCmd("a", a.Id))
	again := h.mustApply(createBucketCmd("a", "alice")).Bucket
	if again.Id != 3 {
		t.Fatalf("re-created bucket id = %d, want 3 (ids are never reused)", again.Id)
	}
}

func TestMarkBucketDeleting(t *testing.T) {
	h := newHarness(t, KindSystem)
	id := h.mustApply(createBucketCmd("a", "alice")).Bucket.Id

	before := h.dumpState()
	wantCode(t, h.mustReject(markDeletingCmd("a", id+1, true)), s3err.NoSuchBucket)
	wantCode(t, h.mustReject(markDeletingCmd("missing", id, true)), s3err.NoSuchBucket)
	h.assertUnchanged(before)

	res := h.mustApply(markDeletingCmd("a", id, true))
	if res.Bucket == nil || !res.Bucket.Deleting {
		t.Fatalf("mark deleting: result %v, want Deleting true", res.Bucket)
	}
	if _, err := h.getBucket("a", false); s3err.CodeOf(err) != s3err.NoSuchBucket {
		t.Fatalf("GetBucket of a deleting bucket: %v, want NoSuchBucket", err)
	}

	res = h.mustApply(markDeletingCmd("a", id, false))
	if res.Bucket.Deleting {
		t.Fatal("unmark deleting: bucket still deleting")
	}
}

func TestDeleteBucket(t *testing.T) {
	h := newHarness(t, KindSystem)
	id := h.mustApply(createBucketCmd("a", "alice")).Bucket.Id

	// Only a bucket marked deleting can be removed.
	before := h.dumpState()
	h.mustReject(deleteBucketCmd("a", id))
	h.assertUnchanged(before)

	h.mustApply(markDeletingCmd("a", id, true))
	wantCode(t, h.mustReject(deleteBucketCmd("a", id+1)), s3err.NoSuchBucket)
	wantCode(t, h.mustReject(deleteBucketCmd("missing", id)), s3err.NoSuchBucket)

	h.mustApply(deleteBucketCmd("a", id))
	if _, err := h.getBucket("a", true); s3err.CodeOf(err) != s3err.NoSuchBucket {
		t.Fatalf("GetBucket after delete (include deleting): %v, want NoSuchBucket", err)
	}
}

func TestSetBucketVersioning(t *testing.T) {
	h := newHarness(t, KindSystem)
	id := h.mustApply(createBucketCmd("a", "alice")).Bucket.Id

	res := h.mustApply(setBucketVersioningCmd("a", id, pb.VersioningState_VERSIONING_STATE_ENABLED))
	if res.Bucket.Versioning != pb.VersioningState_VERSIONING_STATE_ENABLED {
		t.Fatalf("versioning = %v, want ENABLED", res.Bucket.Versioning)
	}

	wantCode(t, h.mustReject(setBucketVersioningCmd("a", id+1, pb.VersioningState_VERSIONING_STATE_SUSPENDED)), s3err.NoSuchBucket)

	h.mustApply(markDeletingCmd("a", id, true))
	before := h.dumpState()
	wantCode(t, h.mustReject(setBucketVersioningCmd("a", id, pb.VersioningState_VERSIONING_STATE_SUSPENDED)), s3err.NoSuchBucket)
	h.assertUnchanged(before)
}

func TestBucketVisibility(t *testing.T) {
	h := newHarness(t, KindSystem)
	h.mustApply(createBucketCmd("c", "u"))
	h.mustApply(createBucketCmd("a", "u"))
	b := h.mustApply(createBucketCmd("b", "u")).Bucket
	h.mustApply(markDeletingCmd("b", b.Id, true))

	var names []string
	for _, rec := range h.listBuckets() {
		names = append(names, rec.Name)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "c" {
		t.Fatalf("ListBuckets = %v, want [a c]", names)
	}

	if _, err := h.getBucket("b", false); s3err.CodeOf(err) != s3err.NoSuchBucket {
		t.Fatalf("GetBucket hides deleting: %v", err)
	}
	got, err := h.getBucket("b", true)
	if err != nil || got.Id != b.Id || !got.Deleting {
		t.Fatalf("GetBucket include_deleting = %v, %v; want the deleting bucket", got, err)
	}
}

// Versioning and the fence are separate fields of the control record: one
// command must not clear the other.
func TestControlFenceAndVersioningAreIndependent(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(fenceCmd(testBucket, true))
	h.mustApply(versioningCmd(testBucket, pb.VersioningState_VERSIONING_STATE_ENABLED))

	wantCode(t, h.mustReject(putCmd(testBucket, objRec("k", "v1", etagA), nil)), s3err.NoSuchBucket)

	h.mustApply(fenceCmd(testBucket, false))
	h.mustApply(putCmd(testBucket, objRec("k", "v1", etagA), nil))
	h.mustReject(putCmd(testBucket, objRec("k", "", etagB), nil)) // still versioned
}

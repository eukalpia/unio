package fsm

import (
	"fmt"
	"testing"

	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestReadObject(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, enabled))
	h.mustApply(putCmd(testBucket, objRec("k", "v1", etagA), nil))
	h.mustApply(putCmd(testBucket, objRec("k", "v2", etagB), nil))

	latest, err := h.getObject("k", "")
	if err != nil || latest.VersionId != "v2" || latest.DeleteMarker {
		t.Fatalf("latest = %v, %v; want v2", latest, err)
	}
	old, err := h.getObject("k", "v1")
	if err != nil || old.Etag != etagA {
		t.Fatalf("v1 = %v, %v; want etag %q", old, err, etagA)
	}

	_, err = h.getObject("missing", "")
	wantCode(t, err, s3err.NoSuchKey)
	_, err = h.getObject("k", "nope")
	wantCode(t, err, s3err.NoSuchVersion)

	// A delete marker is returned as the latest version, flagged.
	h.mustApply(deleteCmd(testBucket, "k", "", "m1", nil))
	marker, err := h.getObject("k", "")
	if err != nil || !marker.DeleteMarker || marker.VersionId != "m1" {
		t.Fatalf("latest after delete = %v, %v; want marker m1", marker, err)
	}
	if got, err := h.getObject("k", "m1"); err != nil || !got.DeleteMarker {
		t.Fatalf("marker by id = %v, %v", got, err)
	}
}

func TestReadUpload(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(createUploadCmd(testBucket, "k", "u1"))

	res, err := h.read(&pb.ReadQuery{Query: &pb.ReadQuery_GetUpload{GetUpload: &pb.GetUploadQuery{
		BucketId: uint64(testBucket),
		Key:      "k",
		UploadId: "u1",
	}}})
	if err != nil || res.GetUpload().GetUpload().GetUploadId() != "u1" {
		t.Fatalf("get upload = %v, %v; want u1", res, err)
	}

	_, err = h.read(&pb.ReadQuery{Query: &pb.ReadQuery_GetUpload{GetUpload: &pb.GetUploadQuery{
		BucketId: uint64(testBucket),
		Key:      "other",
		UploadId: "u1",
	}}})
	wantCode(t, err, s3err.NoSuchUpload)
}

func TestReadListParts(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(createUploadCmd(testBucket, "k", "u1"))
	// Parts arrive out of order; listing must still be in part-number order.
	for _, n := range []uint32{3, 1, 5, 2, 4} {
		h.mustApply(putPartCmd(testBucket, "k", "u1", n, "p", partData(byte(n))))
	}

	list := func(marker, limit uint32) *pb.PartsResult {
		t.Helper()
		res, err := h.read(&pb.ReadQuery{Query: &pb.ReadQuery_ListParts{ListParts: &pb.ListPartsQuery{
			BucketId:         uint64(testBucket),
			Key:              "k",
			UploadId:         "u1",
			PartNumberMarker: marker,
			Limit:            limit,
		}}})
		if err != nil {
			t.Fatalf("list parts after %d: %v", marker, err)
		}
		return res.GetParts()
	}
	numbers := func(p *pb.PartsResult) []uint32 {
		var out []uint32
		for _, part := range p.GetParts() {
			out = append(out, part.Number)
		}
		return out
	}
	equal := func(a, b []uint32) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	tests := []struct {
		name   string
		marker uint32
		limit  uint32
		want   []uint32
		more   bool
	}{
		{name: "first page", marker: 0, limit: 2, want: []uint32{1, 2}, more: true},
		{name: "middle page", marker: 2, limit: 2, want: []uint32{3, 4}, more: true},
		{name: "last page", marker: 4, limit: 2, want: []uint32{5}, more: false},
		{name: "no limit", marker: 0, limit: 0, want: []uint32{1, 2, 3, 4, 5}, more: false},
		{name: "past the end", marker: 5, limit: 2, want: nil, more: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := list(tc.marker, tc.limit)
			if !equal(numbers(got), tc.want) || got.More != tc.more {
				t.Fatalf("parts = %v more=%v; want %v more=%v", numbers(got), got.More, tc.want, tc.more)
			}
		})
	}
}

// Read may run concurrently with Apply. Under -race this checks that the read
// path shares no unsynchronized state with the write path.
func TestReadConcurrentWithApply(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, enabled))
	h.mustApply(putCmd(testBucket, objRec("k0", "v0", etagA), nil))

	const writes = 300
	errs := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		list := &pb.ReadQuery{Query: &pb.ReadQuery_ListObjects{ListObjects: &pb.ListObjectsQuery{
			BucketId: uint64(testBucket),
			Limit:    10,
		}}}
		for i := 0; i < writes; i++ {
			if _, err := h.f.Read(getObjectQuery(testBucket, "k0", "")); err != nil {
				errs <- err
				return
			}
			if _, err := h.f.Read(list); err != nil {
				errs <- err
				return
			}
		}
	}()
	for i := 1; i <= writes; i++ {
		h.mustApply(putCmd(testBucket, objRec("k0", fmt.Sprintf("v%d", i), etagB), nil))
	}
	<-done
	select {
	case err := <-errs:
		t.Fatalf("read during apply: %v", err)
	default:
	}
	latest, err := h.getObject("k0", "")
	if err != nil || latest.VersionId != fmt.Sprintf("v%d", writes) {
		t.Fatalf("latest after writes = %v, %v", latest, err)
	}
}

func TestReadBucketHasData(t *testing.T) {
	h := newHarness(t, KindData)
	hasData := func() bool {
		t.Helper()
		res, err := h.read(&pb.ReadQuery{Query: &pb.ReadQuery_BucketHasData{BucketHasData: &pb.BucketHasDataQuery{
			BucketId: uint64(testBucket),
		}}})
		if err != nil {
			t.Fatalf("bucket has data: %v", err)
		}
		return res.GetBucketHasData().GetHasData()
	}

	if hasData() {
		t.Fatal("empty partition reports data")
	}
	h.mustApply(createUploadCmd(testBucket, "k", "u1"))
	if !hasData() {
		t.Fatal("upload not counted as data")
	}
	h.mustApply(abortCmd(testBucket, "k", "u1"))
	if hasData() {
		t.Fatal("aborted upload still counted")
	}

	h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))
	if !hasData() {
		t.Fatal("object not counted as data")
	}
	h.mustApply(deleteCmd(testBucket, "k", "", "", nil))
	if hasData() {
		t.Fatal("unversioned delete left data behind")
	}

	// Another bucket's data is not this bucket's data.
	h.mustApply(putCmd(otherBucket, objRec("k", "", etagA), nil))
	if hasData() {
		t.Fatal("data of another bucket counted")
	}
}

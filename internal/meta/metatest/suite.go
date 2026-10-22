package metatest

import (
	"testing"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// storeCase is one conformance case. It runs against a fresh store.
type storeCase struct {
	name string
	run  func(t *testing.T, s meta.Store)
}

// kvCase is one conformance case for SystemKV. It runs against a fresh KV.
type kvCase struct {
	name string
	run  func(t *testing.T, kv meta.SystemKV)
}

// RunStoreSuite runs the conformance suite for meta.Store. Each case runs in
// its own subtest against a new store from newStore, so cases never share
// state. The suite checks the semantics documented on meta.Store, on
// object.Conditions and on the s3err codes each operation returns. It uses
// only the public interfaces, so any implementation can run it.
func RunStoreSuite(t *testing.T, newStore func(t *testing.T) meta.Store) {
	t.Helper()
	for _, c := range storeCases() {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, newStore(t))
		})
	}
}

// RunSystemKVSuite runs the conformance suite for meta.SystemKV. Each case
// runs in its own subtest against a new SystemKV from newKV.
func RunSystemKVSuite(t *testing.T, newKV func(t *testing.T) meta.SystemKV) {
	t.Helper()
	for _, c := range kvCases() {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, newKV(t))
		})
	}
}

// storeCases returns every meta.Store case in run order.
func storeCases() []storeCase {
	var cases []storeCase
	cases = append(cases, bucketCases()...)
	cases = append(cases, objectCases()...)
	cases = append(cases, listCases()...)
	cases = append(cases, uploadCases()...)
	cases = append(cases, layoutCases()...)
	return cases
}

// bucketCases covers bucket lifecycle, versioning and missing-bucket errors.
func bucketCases() []storeCase {
	return []storeCase{
		{"Bucket/CreateReturnsInfo", caseCreateReturnsInfo},
		{"Bucket/DuplicateSameOwner", caseDuplicateSameOwner},
		{"Bucket/DuplicateOtherOwner", caseDuplicateOtherOwner},
		{"Bucket/InvalidName", caseInvalidBucketName},
		{"Bucket/GetMissing", caseGetMissingBucket},
		{"Bucket/ListOrderedByName", caseListBucketsOrdered},
		{"Bucket/SetVersioning", caseSetVersioning},
		{"Bucket/DeleteEmpty", caseDeleteEmptyBucket},
		{"Bucket/DeleteMissing", caseDeleteMissingBucket},
		{"Bucket/DeleteNotEmptyObject", caseDeleteNotEmptyObject},
		{"Bucket/DeleteNotEmptyDeleteMarker", caseDeleteNotEmptyDeleteMarker},
		{"Bucket/DeleteNotEmptyUpload", caseDeleteNotEmptyUpload},
		{"Bucket/RecreateGetsNewID", caseRecreateGetsNewID},
		{"Bucket/MissingBucketErrors", caseMissingBucketErrors},
	}
}

func caseCreateReturnsInfo(t *testing.T, s meta.Store) {
	info := mustCreateBucket(t, s, "photos", object.VersioningEnabled)
	if info.Name != "photos" || info.Owner != testOwner || info.Versioning != object.VersioningEnabled {
		t.Fatalf("CreateBucket info = %+v", info)
	}
	if info.ID == 0 {
		t.Fatal("CreateBucket returned a zero BucketID")
	}
	if info.Created.IsZero() {
		t.Fatal("CreateBucket returned a zero Created time")
	}
	got, err := s.GetBucket(t.Context(), "photos")
	mustNoErr(t, err)
	if got.ID != info.ID || got.Owner != info.Owner || got.Versioning != info.Versioning || !got.Created.Equal(info.Created) {
		t.Fatalf("GetBucket = %+v, want %+v", got, info)
	}
}

func caseDuplicateSameOwner(t *testing.T, s meta.Store) {
	first := mustCreateBucket(t, s, "dup-same", object.VersioningUnversioned)
	_, err := s.CreateBucket(t.Context(), "dup-same", object.MakeBucketOptions{Owner: testOwner})
	requireCode(t, err, s3err.BucketAlreadyOwnedByYou)
	got, err := s.GetBucket(t.Context(), "dup-same")
	mustNoErr(t, err)
	if got.ID != first.ID {
		t.Fatalf("failed CreateBucket changed the bucket: id %d, want %d", got.ID, first.ID)
	}
}

func caseDuplicateOtherOwner(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, "dup-other", object.VersioningUnversioned)
	_, err := s.CreateBucket(t.Context(), "dup-other", object.MakeBucketOptions{Owner: "owner-b"})
	requireCode(t, err, s3err.BucketAlreadyExists)
}

func caseInvalidBucketName(t *testing.T, s meta.Store) {
	for _, name := range []string{"ab", "Bad_Name", "-leading", "192.168.0.1", "double..dot", "xn--prefix"} {
		_, err := s.CreateBucket(t.Context(), name, object.MakeBucketOptions{Owner: testOwner})
		if err == nil {
			t.Errorf("CreateBucket(%q) succeeded, want InvalidBucketName", name)
			continue
		}
		requireCode(t, err, s3err.InvalidBucketName)
	}
}

func caseGetMissingBucket(t *testing.T, s meta.Store) {
	_, err := s.GetBucket(t.Context(), "absent")
	requireCode(t, err, s3err.NoSuchBucket)
}

func caseListBucketsOrdered(t *testing.T, s meta.Store) {
	buckets, err := s.ListBuckets(t.Context())
	mustNoErr(t, err)
	requireCount(t, "empty ListBuckets", buckets, 0)

	for _, name := range []string{"zeta-bkt", "alpha-bkt", "mid-bkt"} {
		mustCreateBucket(t, s, name, object.VersioningUnversioned)
	}
	buckets, err = s.ListBuckets(t.Context())
	mustNoErr(t, err)
	names := make([]string, 0, len(buckets))
	for _, b := range buckets {
		names = append(names, b.Name)
	}
	requireEqualStrings(t, "bucket names", names, []string{"alpha-bkt", "mid-bkt", "zeta-bkt"})
}

func caseSetVersioning(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, "ver-bkt", object.VersioningUnversioned)
	for _, state := range []object.VersioningState{object.VersioningEnabled, object.VersioningSuspended, object.VersioningUnversioned} {
		mustNoErr(t, s.SetBucketVersioning(t.Context(), "ver-bkt", state))
		got, err := s.GetBucket(t.Context(), "ver-bkt")
		mustNoErr(t, err)
		if got.Versioning != state {
			t.Fatalf("Versioning = %d after setting %d", got.Versioning, state)
		}
	}
	requireCode(t, s.SetBucketVersioning(t.Context(), "absent", object.VersioningEnabled), s3err.NoSuchBucket)
}

func caseDeleteEmptyBucket(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, "gone", object.VersioningUnversioned)
	mustNoErr(t, s.DeleteBucket(t.Context(), "gone"))
	_, err := s.GetBucket(t.Context(), "gone")
	requireCode(t, err, s3err.NoSuchBucket)
	buckets, err := s.ListBuckets(t.Context())
	mustNoErr(t, err)
	requireCount(t, "buckets after delete", buckets, 0)
}

func caseDeleteMissingBucket(t *testing.T, s meta.Store) {
	requireCode(t, s.DeleteBucket(t.Context(), "absent"), s3err.NoSuchBucket)
}

func caseDeleteNotEmptyObject(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, "full", object.VersioningUnversioned)
	mustPutObject(t, s, objectRecord("full", "k", "", "data"))
	requireCode(t, s.DeleteBucket(t.Context(), "full"), s3err.BucketNotEmpty)
	_, err := s.GetBucket(t.Context(), "full")
	mustNoErr(t, err)
}

func caseDeleteNotEmptyDeleteMarker(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, "markers", object.VersioningEnabled)
	id := newVersionID()
	mustPutObject(t, s, objectRecord("markers", "k", id, "data"))
	_, _, err := s.DeleteObject(t.Context(), "markers", "k", "", object.Conditions{})
	mustNoErr(t, err)
	// Remove the only data version, leaving just the delete marker.
	_, _, err = s.DeleteObject(t.Context(), "markers", "k", id, object.Conditions{})
	mustNoErr(t, err)
	requireCode(t, s.DeleteBucket(t.Context(), "markers"), s3err.BucketNotEmpty)
}

func caseDeleteNotEmptyUpload(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, "uploading", object.VersioningUnversioned)
	mustNoErr(t, s.CreateUpload(t.Context(), uploadRecord("uploading", "k", "u1")))
	requireCode(t, s.DeleteBucket(t.Context(), "uploading"), s3err.BucketNotEmpty)
	_, err := s.AbortUpload(t.Context(), "uploading", "k", "u1")
	mustNoErr(t, err)
	mustNoErr(t, s.DeleteBucket(t.Context(), "uploading"))
}

func caseRecreateGetsNewID(t *testing.T, s meta.Store) {
	first := mustCreateBucket(t, s, "reborn", object.VersioningUnversioned)
	mustNoErr(t, s.DeleteBucket(t.Context(), "reborn"))
	second := mustCreateBucket(t, s, "reborn", object.VersioningUnversioned)
	if second.ID == first.ID {
		t.Fatalf("recreated bucket reused BucketID %d", first.ID)
	}
}

// missingBucketOp is one store operation on a bucket that does not exist.
type missingBucketOp struct {
	name string
	op   func(s meta.Store) error
}

func caseMissingBucketErrors(t *testing.T, s meta.Store) {
	const b = "nowhere"
	ctx := t.Context()
	ops := []missingBucketOp{
		{"DeleteBucket", func(s meta.Store) error { return s.DeleteBucket(ctx, b) }},
		{"SetBucketVersioning", func(s meta.Store) error { return s.SetBucketVersioning(ctx, b, 1) }},
		{"PutObject", func(s meta.Store) error {
			_, err := s.PutObject(ctx, objectRecord(b, "k", "", "x"), object.Conditions{})
			return err
		}},
		{"GetObject", func(s meta.Store) error {
			_, err := s.GetObject(ctx, b, "k", "")
			return err
		}},
		{"DeleteObject", func(s meta.Store) error {
			_, _, err := s.DeleteObject(ctx, b, "k", "", object.Conditions{})
			return err
		}},
		{"ListObjects", func(s meta.Store) error {
			_, err := s.ListObjects(ctx, b, object.ListOptions{})
			return err
		}},
		{"UpdateLayout", func(s meta.Store) error {
			return s.UpdateLayout(ctx, b, "k", "", 0, ids.DataID{}, testLayout())
		}},
		{"CreateUpload", func(s meta.Store) error { return s.CreateUpload(ctx, uploadRecord(b, "k", "u")) }},
		{"GetUpload", func(s meta.Store) error {
			_, err := s.GetUpload(ctx, b, "k", "u")
			return err
		}},
		{"PutPart", func(s meta.Store) error {
			_, err := s.PutPart(ctx, b, "k", "u", partRecord(1, "p"))
			return err
		}},
		{"ListParts", func(s meta.Store) error {
			_, _, err := s.ListParts(ctx, b, "k", "u", object.ListPartsOptions{})
			return err
		}},
		{"CompleteUpload", func(s meta.Store) error {
			_, _, err := s.CompleteUpload(ctx, "u", objectRecord(b, "k", "", "x"), object.Conditions{})
			return err
		}},
		{"AbortUpload", func(s meta.Store) error {
			_, err := s.AbortUpload(ctx, b, "k", "u")
			return err
		}},
		{"ListUploads", func(s meta.Store) error {
			_, err := s.ListUploads(ctx, b, object.ListUploadsOptions{})
			return err
		}},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			requireCode(t, op.op(s), s3err.NoSuchBucket)
		})
	}
}

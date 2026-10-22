package engine_test

import (
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3err"
)

// TestNullVersionNamesTheCurrentObjectOfUnversionedBucket checks that the null
// version id addresses the one version of an object in a bucket that has never
// been versioned, for reads and for a delete, as S3 does.
func TestNullVersionNamesTheCurrentObjectOfUnversionedBucket(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("data")), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}

	r, err := h.GetObject(ctx, testBucket, "k", object.GetOptions{VersionID: "null"})
	if err != nil {
		t.Fatalf("get null version: %v", err)
	}
	if got := readAll(t, r); string(got) != "data" {
		t.Fatalf("get null version = %q, want %q", got, "data")
	}
	if _, err := h.HeadObject(ctx, testBucket, "k", object.GetOptions{VersionID: "null"}); err != nil {
		t.Fatalf("head null version: %v", err)
	}

	res, err := h.DeleteObject(ctx, testBucket, "k", object.DeleteOptions{VersionID: "null"})
	if err != nil {
		t.Fatalf("delete null version: %v", err)
	}
	if res.VersionID != "null" {
		t.Fatalf("delete reports version %q, want the requested %q", res.VersionID, "null")
	}
	if _, err := h.HeadObject(ctx, testBucket, "k", object.GetOptions{}); !s3err.Is(err, s3err.NoSuchKey) {
		t.Fatalf("head after deleting the null version: %v, want NoSuchKey", err)
	}
}

// TestNullVersionInDeleteObjects checks that DeleteObjects accepts the null
// version id for keys of an unversioned bucket, and reports it back.
func TestNullVersionInDeleteObjects(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "b"} {
		if _, err := h.PutObject(ctx, testBucket, k, objecttest.NewBody([]byte(k)), object.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := h.DeleteObjects(ctx, testBucket, []object.ToDelete{
		{Key: "a", VersionID: "null"},
		{Key: "b", VersionID: "null"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("delete %s: %v", r.Key, r.Err)
		}
		if r.VersionID != "null" {
			t.Fatalf("delete %s reports version %q, want %q", r.Key, r.VersionID, "null")
		}
	}
	for _, k := range []string{"a", "b"} {
		if _, err := h.HeadObject(ctx, testBucket, k, object.GetOptions{}); !s3err.Is(err, s3err.NoSuchKey) {
			t.Fatalf("head %s after delete: %v, want NoSuchKey", k, err)
		}
	}
}

// TestNullVersionIsAbsentFromVersionedBucket checks that a versioned bucket has
// no null version: a request for it names an unknown version.
func TestNullVersionIsAbsentFromVersionedBucket(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o", Versioning: object.VersioningEnabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("v1")), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.GetObject(ctx, testBucket, "k", object.GetOptions{VersionID: "null"}); !s3err.Is(err, s3err.NoSuchVersion) {
		t.Fatalf("get null version of a versioned bucket: %v, want NoSuchVersion", err)
	}
}

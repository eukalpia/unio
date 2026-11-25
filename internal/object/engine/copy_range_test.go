package engine_test

import (
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3err"
)

// TestCopyPartRangeMustLieInsideTheSource checks that UploadPartCopy refuses a
// source range that reaches past the end of the source with InvalidRange, where
// a GET would stop at the end, and accepts a range that ends on the last byte.
func TestCopyPartRangeMustLieInsideTheSource(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "src", objecttest.NewBody([]byte("hello")), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	id, err := h.NewMultipartUpload(ctx, testBucket, "dst", object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src := object.Ref{Bucket: testBucket, Key: "src"}

	past := object.CopyOptions{SourceRange: &object.HTTPRange{Start: 0, End: 21}}
	if _, err := h.CopyObjectPart(ctx, src, testBucket, "dst", id, 1, past); s3err.CodeOf(err) != s3err.InvalidRange {
		t.Fatalf("range past the end: %v, want InvalidRange", err)
	}

	last := object.CopyOptions{SourceRange: &object.HTTPRange{Start: 0, End: 4}}
	if _, err := h.CopyObjectPart(ctx, src, testBucket, "dst", id, 1, last); err != nil {
		t.Fatalf("range ending on the last byte: %v", err)
	}
}

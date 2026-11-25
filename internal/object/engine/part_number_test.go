package engine_test

import (
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3err"
)

// noSuchPartInfo is the interface a front end uses to learn that a part number
// named no part of the object.
type noSuchPartInfo interface {
	NoSuchPart() bool
}

// TestPartNumberBeyondTheObjectNamesNoPart checks that a GetObject or HeadObject
// part number that names no part fails with InvalidArgument, whose cause tells
// the front end that the part does not exist, for a single-part object and for
// a multipart object. A part that exists is served, and a negative number is
// malformed without that cause.
func TestPartNumberBeyondTheObjectNamesNoPart(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "single", objecttest.NewBody([]byte("one")), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}

	id, err := h.NewMultipartUpload(ctx, testBucket, "multi", object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var parts []object.CompletePart
	for i, data := range [][]byte{payloadOf(5<<20, 1), []byte("bb")} {
		info, err := h.PutObjectPart(ctx, testBucket, "multi", id, i+1, objecttest.NewBody(data))
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, object.CompletePart{PartNumber: i + 1, ETag: info.ETag})
	}
	if _, err := h.CompleteMultipartUpload(ctx, testBucket, "multi", id, parts, object.PutOptions{}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		key    string
		number int
		noPart bool
		want   s3err.Code
	}{
		{name: "single part object, part 1", key: "single", number: 1},
		{name: "single part object, part 2", key: "single", number: 2, noPart: true, want: s3err.InvalidArgument},
		{name: "single part object, far beyond", key: "single", number: 10001, noPart: true, want: s3err.InvalidArgument},
		{name: "multipart object, part 2", key: "multi", number: 2},
		{name: "multipart object, part 3", key: "multi", number: 3, noPart: true, want: s3err.InvalidArgument},
		{name: "negative number is malformed", key: "multi", number: -1, want: s3err.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := object.GetOptions{PartNumber: tt.number}
			_, err := h.HeadObject(ctx, testBucket, tt.key, opts)
			checkPartError(t, "head", err, tt.want, tt.noPart)
			r, err := h.GetObject(ctx, testBucket, tt.key, opts)
			checkPartError(t, "get", err, tt.want, tt.noPart)
			if err == nil {
				readAll(t, r)
			}
		})
	}
}

// checkPartError checks the code of a part read error, and whether its cause
// reports a missing part.
func checkPartError(t *testing.T, op string, err error, want s3err.Code, noPart bool) {
	t.Helper()
	if got := s3err.CodeOf(err); got != want {
		t.Fatalf("%s: code %q (err %v), want %q", op, got, err, want)
	}
	var np noSuchPartInfo
	if got := errors.As(err, &np) && np.NoSuchPart(); got != noPart {
		t.Fatalf("%s: missing-part cause = %v, want %v (err %v)", op, got, noPart, err)
	}
}

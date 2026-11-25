package engine_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure/erasuretest"
	"github.com/eukalpia/unio/internal/erasure/rs"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/engine/internal/testbackend"
	"github.com/eukalpia/unio/internal/object/objecttest"
)

// TestDisabledInlineTierErasureCodesEveryObject checks that a negative
// InlineThreshold stores every object, zero-byte ones included, as one
// erasure-coded part, and that the data reads back exactly. Deleting the
// objects must reclaim their data.
func TestDisabledInlineTierErasureCodesEveryObject(t *testing.T) {
	h := newHarness(t, engine.Options{InlineThreshold: -1})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	sizes := []int{0, 1, 4 << 10}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			key := fmt.Sprintf("k-%d", size)
			data := payloadOf(size, 7)
			before := h.reg.StoredData()
			if _, err := h.PutObject(ctx, testBucket, key, objecttest.NewBody(data), object.PutOptions{}); err != nil {
				t.Fatalf("put: %v", err)
			}
			rec := h.recordOf(t, testBucket, key)
			if rec.Inline != nil {
				t.Fatalf("stored inline (%d bytes), want erasure-coded", len(rec.Inline))
			}
			if len(rec.Parts) != 1 || rec.Parts[0].Size != int64(size) || rec.Parts[0].Data.IsZero() {
				t.Fatalf("parts = %+v, want one part of %d bytes", rec.Parts, size)
			}
			// The test registry is a 2+1 erasure set.
			if l := rec.Parts[0].Layout; l.DataShards != 2 || l.ParityShards != 1 {
				t.Fatalf("layout shape %d+%d, want 2+1", l.DataShards, l.ParityShards)
			}
			if got := mustReadKey(t, h, key); !bytes.Equal(got, data) {
				t.Fatalf("read back %d bytes, want %d equal bytes", len(got), len(data))
			}
			if n := h.reg.StoredData(); n != before+1 {
				t.Fatalf("stored data units = %d, want %d", n, before+1)
			}
		})
	}

	// A copy takes the same route as a put.
	if _, err := h.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "k-0"}, testBucket, "copy-0", object.CopyOptions{}); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if rec := h.recordOf(t, testBucket, "copy-0"); rec.Inline != nil || len(rec.Parts) != 1 {
		t.Fatalf("copy stored inline=%v with %d parts, want one erasure-coded part", rec.Inline != nil, len(rec.Parts))
	}

	for _, key := range []string{"k-0", "k-1", "k-4096", "copy-0"} {
		if _, err := h.DeleteObject(ctx, testBucket, key, object.DeleteOptions{}); err != nil {
			t.Fatalf("delete %q: %v", key, err)
		}
	}
	if err := h.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.reg.StoredData(); n != 0 {
		t.Fatalf("stored data units after deleting every object = %d, want 0", n)
	}
}

// TestDefaultInlineTierKeepsEmptyObjectsInline is the counterpart of the test
// above: with the default threshold a zero-byte object stays in metadata.
func TestDefaultInlineTierKeepsEmptyObjectsInline(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "empty", objecttest.NewBody(nil), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if rec := h.recordOf(t, testBucket, "empty"); rec.Inline == nil || len(rec.Parts) != 0 {
		t.Fatalf("empty object: inline=%v parts=%d, want inline", rec.Inline != nil, len(rec.Parts))
	}
	if h.reg.StoredData() != 0 {
		t.Fatal("inline object wrote erasure data")
	}
}

// TestDisabledInlineTierWithReedSolomonSet runs the empty and one-byte objects
// through the production erasure set, not the in-memory test backend.
func TestDisabledInlineTierWithReedSolomonSet(t *testing.T) {
	const k, m = 2, 1
	mem := make([]*erasuretest.Drive, k+m)
	drives := make([]drive.Drive, k+m)
	for i := range mem {
		mem[i] = erasuretest.NewDrive()
		drives[i] = mem[i]
	}
	set, err := rs.New(ids.SetID(1), drives, k, m, rs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	store := testbackend.NewStore()
	e, err := engine.New(store, erasuretest.NewFakeRegistry(set), engine.Options{InlineThreshold: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.Close(context.Background()); err != nil {
			t.Errorf("close engine: %v", err)
		}
		for _, d := range mem {
			_ = d.Close()
		}
	})
	ctx := t.Context()
	if _, err := e.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, 1} {
		key := fmt.Sprintf("k-%d", size)
		data := payloadOf(size, 9)
		if _, err := e.PutObject(ctx, testBucket, key, objecttest.NewBody(data), object.PutOptions{}); err != nil {
			t.Fatalf("size %d: put: %v", size, err)
		}
		rec, err := store.GetObject(ctx, testBucket, key, "")
		if err != nil {
			t.Fatal(err)
		}
		if rec.Inline != nil || len(rec.Parts) != 1 {
			t.Fatalf("size %d: inline=%v parts=%d, want one erasure-coded part", size, rec.Inline != nil, len(rec.Parts))
		}
		r, err := e.GetObject(ctx, testBucket, key, object.GetOptions{})
		if err != nil {
			t.Fatalf("size %d: get: %v", size, err)
		}
		if got := readAll(t, r); !bytes.Equal(got, data) {
			t.Fatalf("size %d: read back %v, want %v", size, got, data)
		}
	}
}

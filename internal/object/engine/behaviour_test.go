package engine_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/eukalpia/unio/internal/object/engine/digest"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/engine/internal/testbackend"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3err"
)

const testBucket = "engine-tests"

// deleteMarkerInfo is the interface a front end uses to read the marker of a
// NoSuchKey error.
type deleteMarkerInfo interface {
	DeleteMarkerInfo() object.Info
}

func payloadOf(size int, seed byte) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = seed + byte(i%251)
	}
	return b
}

func TestDeleteMarkerIsReportedThroughTheError(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o", Versioning: object.VersioningEnabled}); err != nil {
		t.Fatal(err)
	}
	v1, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("v1")), object.PutOptions{})
	if err != nil || v1.VersionID == "" {
		t.Fatalf("put v1: %+v, %v", v1, err)
	}
	v2, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("v2")), object.PutOptions{})
	if err != nil || v2.VersionID == "" || v2.VersionID == v1.VersionID {
		t.Fatalf("put v2: %+v, %v", v2, err)
	}
	old, err := h.GetObject(ctx, testBucket, "k", object.GetOptions{VersionID: v1.VersionID})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, old); string(got) != "v1" {
		t.Fatalf("old version = %q", got)
	}

	res, err := h.DeleteObject(ctx, testBucket, "k", object.DeleteOptions{})
	if err != nil || !res.DeleteMarker || res.DeleteMarkerVersionID == "" {
		t.Fatalf("delete: %+v, %v", res, err)
	}
	_, err = h.GetObject(ctx, testBucket, "k", object.GetOptions{})
	if !s3err.Is(err, s3err.NoSuchKey) {
		t.Fatalf("get after delete: %v, want NoSuchKey", err)
	}
	var dm deleteMarkerInfo
	if !errors.As(err, &dm) {
		t.Fatal("NoSuchKey does not carry the delete marker")
	}
	if info := dm.DeleteMarkerInfo(); !info.DeleteMarker || info.VersionID != res.DeleteMarkerVersionID {
		t.Fatalf("marker info = %+v, want version %s", info, res.DeleteMarkerVersionID)
	}
	_, err = h.HeadObject(ctx, testBucket, "k", object.GetOptions{})
	if !errors.As(err, &dm) {
		t.Fatalf("head after delete: %v", err)
	}

	_, err = h.GetObject(ctx, testBucket, "k", object.GetOptions{VersionID: res.DeleteMarkerVersionID})
	if !s3err.Is(err, s3err.MethodNotAllowed) {
		t.Fatalf("explicit delete marker read: %v, want MethodNotAllowed", err)
	}
	listing, err := h.ListObjects(ctx, testBucket, object.ListOptions{})
	if err != nil || len(listing.Objects) != 0 {
		t.Fatalf("listing shows a key whose latest version is a delete marker: %+v", listing)
	}
}

func TestInlineThresholdOption(t *testing.T) {
	h := newHarness(t, engine.Options{InlineThreshold: 100})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{100, 101} {
		if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody(payloadOf(size, 1)), object.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := h.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		rec := h.recordOf(t, testBucket, "k")
		wantInline := size <= 100
		if (rec.Inline != nil) != wantInline {
			t.Fatalf("size %d: inline = %v, want %v", size, rec.Inline != nil, wantInline)
		}
		if !wantInline && h.reg.StoredData() != 1 {
			t.Fatalf("size %d: stored data units = %d, want 1", size, h.reg.StoredData())
		}
	}
}

func TestOptionsValidation(t *testing.T) {
	reg := testbackend.NewRegistry(1, 2, 1)
	tests := []struct {
		name string
		opts engine.Options
	}{
		{"threshold above maximum", engine.Options{InlineThreshold: object.MaxInlineThreshold + 1}},
		{"negative threshold", engine.Options{InlineThreshold: -1}},
		{"budget below threshold", engine.Options{InlineThreshold: 4096, BufferBudget: 1024}},
		{"negative gc queue", engine.Options{GCQueueSize: -1}},
		{"negative gc workers", engine.Options{GCWorkers: -1}},
		{"negative delete concurrency", engine.Options{DeleteConcurrency: -2}},
		{"negative write concurrency", engine.Options{MaxConcurrentWrites: -2}},
		{"negative gc timeout", engine.Options{GCTimeout: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := engine.New(testbackend.NewStore(), reg, tt.opts); err == nil {
				t.Fatal("New accepted invalid options")
			}
		})
	}
	if _, err := engine.New(nil, reg, engine.Options{}); err == nil {
		t.Fatal("New accepted a nil store")
	}
	if _, err := engine.New(testbackend.NewStore(), nil, engine.Options{}); err == nil {
		t.Fatal("New accepted a nil registry")
	}
}

// TestCorruptShardsAreNeverServed exercises I2: a read that meets a damaged
// copy falls back to a healthy one, and a read that cannot be served correctly
// fails instead of returning garbage. Only the windows that touch the damaged
// part are affected.
func TestCorruptShardsAreNeverServed(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	p1 := payloadOf(5<<20, 3)
	p2 := payloadOf(64, 9)
	id, err := h.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var parts []object.CompletePart
	for i, data := range [][]byte{p1, p2} {
		info, err := h.PutObjectPart(ctx, testBucket, "k", id, i+1, objecttest.NewBody(data))
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, object.CompletePart{PartNumber: i + 1, ETag: info.ETag})
	}
	if _, err := h.CompleteMultipartUpload(ctx, testBucket, "k", id, parts, object.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	rec := h.recordOf(t, testBucket, "k")
	set, err := h.reg.Get(rec.Parts[1].Layout.Set)
	if err != nil {
		t.Fatal(err)
	}
	bad := set.(*testbackend.Set)

	// One damaged copy of part 2: the window is still served from a copy.
	if !bad.Corrupt(rec.Parts[1].Data, 0) {
		t.Fatal("no shard copy to damage")
	}
	if got := mustReadRange(t, h, "k", 0, int64(len(p1)+len(p2))); !bytes.Equal(got, append(bytes.Clone(p1), p2...)) {
		t.Fatal("a single damaged copy changed the served bytes")
	}

	// Every copy of part 2 is damaged. Reads that stay in part 1 still work;
	// reads that reach part 2 fail and never return damaged bytes. Corrupt
	// flips a byte, so drive 0 (already damaged above) is left alone.
	for i := 1; i < 3; i++ {
		bad.Corrupt(rec.Parts[1].Data, i)
	}
	if got := mustReadRange(t, h, "k", 0, int64(len(p1))); !bytes.Equal(got, p1) {
		t.Fatal("part 1 unreadable although only part 2 is damaged")
	}
	r, err := h.GetObject(ctx, testBucket, "k", object.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	_ = r.Close()
	if !errors.Is(err, erasure.ErrCorrupt) {
		t.Fatalf("read of damaged part: err = %v, want ErrCorrupt", err)
	}
	full := append(bytes.Clone(p1), p2...)
	if !bytes.HasPrefix(full, got) {
		t.Fatal("the reader returned bytes that differ from the written data")
	}
}

// TestBodyLongerThanDeclaredLeavesNothing covers a body that yields more bytes
// than it declared. The write must fail and nothing may become visible, and
// the shards already written must be reclaimed (I4).
func TestBodyLongerThanDeclaredLeavesNothing(t *testing.T) {
	for _, size := range []int{5, 5000} {
		t.Run(sizeLabel(size), func(t *testing.T) {
			h := newHarness(t, engine.Options{InlineThreshold: 64})
			ctx := t.Context()
			if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
				t.Fatal(err)
			}
			body := &longBody{data: payloadOf(size+1, 5), declared: int64(size)}
			_, err := h.PutObject(ctx, testBucket, "k", body, object.PutOptions{})
			if !s3err.Is(err, s3err.IncompleteBody) {
				t.Fatalf("err = %v, want IncompleteBody", err)
			}
			if _, err := h.HeadObject(ctx, testBucket, "k", object.GetOptions{}); !s3err.Is(err, s3err.NoSuchKey) {
				t.Fatalf("object visible after a failed body: %v", err)
			}
			if err := h.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if n := h.reg.StoredData(); n != 0 {
				t.Fatalf("%d data units left behind", n)
			}
		})
	}
}

// TestWriteSlotWaitHonoursContext checks that a write waiting for a slot
// returns when its context ends, and that the slot holder is unaffected.
func TestWriteSlotWaitHonoursContext(t *testing.T) {
	h := newHarness(t, engine.Options{MaxConcurrentWrites: 1, InlineThreshold: 64})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	started := make(chan struct{})
	holder := &gatedBody{data: payloadOf(200, 1), gate: gate, started: started}
	holderDone := make(chan error, 1)
	go func() {
		_, err := h.PutObject(ctx, testBucket, "holder", holder, object.PutOptions{})
		holderDone <- err
	}()
	<-started // the holder now owns the only write slot

	waitCtx, cancel := context.WithCancel(ctx)
	waiterDone := make(chan error, 1)
	go func() {
		_, err := h.PutObject(waitCtx, testBucket, "waiter", objecttest.NewBody(payloadOf(200, 2)), object.PutOptions{})
		waiterDone <- err
	}()
	cancel()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter: err = %v, want context.Canceled", err)
	}

	close(gate)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder: %v", err)
	}
	if got := mustReadRange(t, h, "holder", 0, 200); !bytes.Equal(got, holder.data) {
		t.Fatal("holder content differs")
	}
}

// TestDeleteBucketForceAbortsUploads checks that forced bucket deletion also
// reclaims the parts of uploads that are still in progress.
func TestDeleteBucketForceAbortsUploads(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	id, err := h.NewMultipartUpload(ctx, testBucket, "pending", object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObjectPart(ctx, testBucket, "pending", id, 1, objecttest.NewBody(payloadOf(1000, 4))); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "obj", objecttest.NewBody(payloadOf(5000, 6)), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteBucket(ctx, testBucket, object.DeleteBucketOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.reg.StoredData(); n != 0 {
		t.Fatalf("%d data units survived forced deletion", n)
	}
}

// TestReaderCloseSemantics checks that a closed reader refuses to read and
// that closing twice is harmless.
func TestReaderCloseSemantics(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody(payloadOf(5000, 8)), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	rng := object.HTTPRange{Start: 0, End: 9}
	r, err := h.GetObject(ctx, testBucket, "k", object.GetOptions{Range: &rng})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := r.Read(make([]byte, 4)); err == nil {
		t.Fatal("read after close succeeded")
	}
}

// longBody yields size+1 bytes although it declares size bytes.
type longBody struct {
	data     []byte
	declared int64
	pos      int
}

func (b *longBody) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	if b.pos >= len(b.data) {
		return n, io.EOF
	}
	return n, nil
}

func (b *longBody) Size() int64               { return b.declared }
func (b *longBody) MD5() [16]byte             { return [16]byte{} }
func (b *longBody) Checksum() object.Checksum { return object.Checksum{} }

// gatedBody delivers data once gate is closed and reports when it first runs.
type gatedBody struct {
	data    []byte
	gate    chan struct{}
	started chan struct{}
	once    bool
	pos     int
}

func (b *gatedBody) Read(p []byte) (int, error) {
	if !b.once {
		b.once = true
		close(b.started)
		<-b.gate
	}
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	if b.pos >= len(b.data) {
		return n, io.EOF
	}
	return n, nil
}

func (b *gatedBody) Size() int64 { return int64(len(b.data)) }
func (b *gatedBody) MD5() [16]byte {
	return digest.SumMD5(b.data)
}
func (b *gatedBody) Checksum() object.Checksum { return object.Checksum{} }

// readAll reads a reader to the end and closes it.
func readAll(t *testing.T, r *object.Reader) []byte {
	t.Helper()
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// mustReadRange returns bytes [off, off+length) of key.
func mustReadRange(t *testing.T, h *harness, key string, off, length int64) []byte {
	t.Helper()
	rng := object.HTTPRange{Start: off, End: off + length - 1}
	r, err := h.GetObject(t.Context(), testBucket, key, object.GetOptions{Range: &rng})
	if err != nil {
		t.Fatal(err)
	}
	return readAll(t, r)
}

func sizeLabel(n int) string {
	if n < 100 {
		return "small"
	}
	return "erasure"
}

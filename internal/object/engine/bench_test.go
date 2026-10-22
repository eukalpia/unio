package engine_test

import (
	"crypto/md5"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
)

// benchBody is a payload that is reset and reused between iterations, so that
// the allocations a benchmark reports belong to the engine.
type benchBody struct {
	data []byte
	sum  [16]byte
	pos  int
}

func newBenchBody(data []byte) *benchBody {
	return &benchBody{data: data, sum: md5.Sum(data)}
}

func (b *benchBody) Read(p []byte) (int, error) {
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

func (b *benchBody) Size() int64               { return int64(len(b.data)) }
func (b *benchBody) MD5() [16]byte             { return b.sum }
func (b *benchBody) Checksum() object.Checksum { return object.Checksum{} }

func benchEngine(b *testing.B) *engine.Engine {
	b.Helper()
	h := newHarness(b, engine.Options{GCQueueSize: 1 << 16})
	if _, err := h.MakeBucket(b.Context(), testBucket, object.MakeBucketOptions{Owner: "bench"}); err != nil {
		b.Fatal(err)
	}
	return h.Engine
}

func benchmarkPut(b *testing.B, size int) {
	e := benchEngine(b)
	body := newBenchBody(payloadOf(size, 7))
	ctx := b.Context()
	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		body.pos = 0
		if _, err := e.PutObject(ctx, testBucket, "bench", body, object.PutOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkGet(b *testing.B, size int) {
	e := benchEngine(b)
	ctx := b.Context()
	if _, err := e.PutObject(ctx, testBucket, "bench", newBenchBody(payloadOf(size, 7)), object.PutOptions{}); err != nil {
		b.Fatal(err)
	}
	buf := make([]byte, 64<<10)
	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r, err := e.GetObject(ctx, testBucket, "bench", object.GetOptions{})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.CopyBuffer(io.Discard, r, buf); err != nil {
			b.Fatal(err)
		}
		if err := r.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPutInline2KiB(b *testing.B)  { benchmarkPut(b, 2<<10) }
func BenchmarkGetInline2KiB(b *testing.B)  { benchmarkGet(b, 2<<10) }
func BenchmarkPutErasure1MiB(b *testing.B) { benchmarkPut(b, 1<<20) }
func BenchmarkGetErasure1MiB(b *testing.B) { benchmarkGet(b, 1<<20) }

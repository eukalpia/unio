package s3api_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/s3api"
)

// BenchmarkGetObjectStream measures a 4 MiB GET over a keep-alive connection
// on loopback. The body is streamed through the pooled copy buffer, so the
// reported bytes per second and allocations per operation track that path.
func BenchmarkGetObjectStream(b *testing.B) {
	const size = 4 << 20
	base := seedObject(b, s3api.Config{}, "bench", "obj", patterned(size))
	hc := newHTTPClient(b, nil)
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/bench/obj", nil)
		if err != nil {
			b.Fatal(err)
		}
		req.Header.Set("Authorization", "x")
		resp, err := hc.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		n, err := io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if err != nil || n != size {
			b.Fatalf("read %d bytes, err %v", n, err)
		}
	}
}

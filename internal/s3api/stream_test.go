package s3api_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/s3api"
)

// get performs one GET with a deadline, so that a leaked copy buffer fails the
// test instead of hanging it. It returns the status and the whole body.
func get(t *testing.T, base, path string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "x")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := newHTTPClient(t, nil).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v (a copy buffer may not have been released)", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return resp, body
}

// TestCopyBufferIsReleasedOnEveryPath runs the GET paths with a budget of
// exactly one copy buffer. Any path that keeps its buffer makes the next
// request wait for it, and the deadline in get reports the leak. The paths are
// full and range reads, a conditional 304, a missing key, an unsatisfiable
// range and a stream cut by a stalled client.
func TestCopyBufferIsReleasedOnEveryPath(t *testing.T) {
	const size = 8 << 20
	budget := bufpool.NewBudget(64 << 10)
	cfg := s3api.Config{BufferBudget: budget, WriteIdleTimeout: 200 * time.Millisecond}
	base := seedObject(t, cfg, "pool", "obj", patterned(size))

	full, body := get(t, base, "/pool/obj", nil)
	if full.StatusCode != http.StatusOK || len(body) != size {
		t.Fatalf("full GET: status %d, %d bytes", full.StatusCode, len(body))
	}
	etag := full.Header.Get("ETag")

	if resp, body := get(t, base, "/pool/obj", map[string]string{"Range": "bytes=0-9"}); resp.StatusCode != http.StatusPartialContent || len(body) != 10 {
		t.Fatalf("range GET: status %d, %d bytes", resp.StatusCode, len(body))
	}
	if resp, _ := get(t, base, "/pool/obj", map[string]string{"If-None-Match": etag}); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET: status %d", resp.StatusCode)
	}
	if resp, _ := get(t, base, "/pool/missing", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing GET: status %d", resp.StatusCode)
	}
	if resp, _ := get(t, base, "/pool/obj", map[string]string{"Range": "bytes=99999999-"}); resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("unsatisfiable range: status %d", resp.StatusCode)
	}

	// A client that reads the head and then stalls gets its stream cut.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/pool/obj", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "x")
	stalled, err := newHTTPClient(t, nil).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(stalled.Body, make([]byte, 4<<10)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // stall for several idle periods so the server cuts the stream
	if _, err := io.Copy(io.Discard, stalled.Body); err == nil {
		t.Fatal("stalled download was not cut")
	}
	_ = stalled.Body.Close()

	for i := 0; i < 2; i++ {
		if _, body := get(t, base, "/pool/obj", nil); !bytes.Equal(body, patterned(size)) {
			t.Fatalf("full GET %d after the cut differs from the object", i)
		}
	}
}

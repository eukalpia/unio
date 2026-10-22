package s3api_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/s3api"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
	"github.com/eukalpia/unio/internal/s3err"
)

// observation is one call of the recording observer.
type observation struct {
	op      string
	status  int
	d       time.Duration
	in, out int64
}

// recorder is an s3api.Observer that keeps every call. It is safe for
// concurrent use.
type recorder struct {
	mu    sync.Mutex
	calls []observation
}

// ObserveHTTPRequest implements s3api.Observer.
func (r *recorder) ObserveHTTPRequest(op string, status int, d time.Duration, in, out int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, observation{op: op, status: status, d: d, in: in, out: out})
}

// reset forgets the calls so far, for example those of seeding requests.
func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
}

// count returns the number of calls so far.
func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// waitCount waits until n calls have arrived, then waits a little longer and
// fails unless exactly n arrived. A second call for one request would show up
// in that wait. The observer runs on the request goroutine, so a streamed
// response can reach the client before its call, which is why it waits.
func (r *recorder) waitCount(t testing.TB, n int) []observation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for r.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("observer got %d calls, want %d", r.count(), n)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) != n {
		t.Fatalf("observer got %d calls, want exactly %d: %+v", len(r.calls), n, r.calls)
	}
	return append([]observation(nil), r.calls...)
}

// only waits for the single call of a one-request test.
func (r *recorder) only(t testing.TB) observation {
	t.Helper()
	return r.waitCount(t, 1)[0]
}

// doRaw sends one request with the given headers and body, and returns the
// status and the whole response body. It does not call t, so it may run on
// another goroutine.
func doRaw(hc *http.Client, method, url string, headers map[string]string, body []byte) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, r)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "x")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// TestObserverReportsEachRequest sends one request of each kind and checks
// the single call it produces: the operation, the status, and the bytes read
// and written. A 1 MiB GET is streamed through the copy buffer.
func TestObserverReportsEachRequest(t *testing.T) {
	const size = 1 << 20
	rec := &recorder{}
	base := seedObject(t, s3api.Config{Observer: rec}, "bkt", "k", patterned(size))
	rec.reset()

	cases := []struct {
		name, method, path string
		headers            map[string]string
		body               []byte
		wantOp             string
		wantStatus         int
		wantIn             int64
		// wantOut is the body length, or -1 to take the length of the body the
		// client received.
		wantOut int64
	}{
		{"put", http.MethodPut, "/bkt/put", nil, patterned(4096), "PutObject", 200, 4096, 0},
		{"get streamed", http.MethodGet, "/bkt/k", nil, nil, "GetObject", 200, 0, size},
		{"get range", http.MethodGet, "/bkt/k", map[string]string{"Range": "bytes=0-9"}, nil, "GetObject", 206, 0, 10},
		{"head", http.MethodHead, "/bkt/k", nil, nil, "HeadObject", 200, 0, 0},
		{"error", http.MethodGet, "/bkt/missing", nil, nil, "GetObject", 404, 0, -1},
		{"unrouted", http.MethodGet, "/unio/nothing", nil, nil, "Unknown", 404, 0, -1},
		{"health live", http.MethodGet, "/unio/health/live", nil, nil, "Health", 200, 0, 2},
		{"health ready", http.MethodGet, "/unio/health/ready", nil, nil, "Health", 200, 0, 2},
	}
	hc := newHTTPClient(t, nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec.reset()
			status, body, err := doRaw(hc, tc.method, base+tc.path, tc.headers, tc.body)
			if err != nil {
				t.Fatal(err)
			}
			if status != tc.wantStatus {
				t.Fatalf("client status = %d, want %d", status, tc.wantStatus)
			}
			got := rec.only(t)
			want := observation{op: tc.wantOp, status: tc.wantStatus, in: tc.wantIn, out: tc.wantOut}
			if tc.wantOut == -1 {
				want.out = int64(len(body))
			}
			if got.op != want.op || got.status != want.status || got.in != want.in || got.out != want.out {
				t.Fatalf("observed %+v, want op %q status %d in %d out %d", got, want.op, want.status, want.in, want.out)
			}
			if got.d <= 0 {
				t.Fatalf("duration = %v, want positive", got.d)
			}
			if tc.name == "get streamed" && !bytes.Equal(body, patterned(size)) {
				t.Fatal("streamed body differs from the object")
			}
		})
	}
}

// TestObserverAbortedStream stalls a client after the first 4 KiB of an 8 MiB
// download. The write idle timeout cuts the stream, and the one call reports
// what the server wrote: at least what the client read and less than the
// object. Its duration covers the stall that ended the stream.
func TestObserverAbortedStream(t *testing.T) {
	const size = 8 << 20
	const idle = 200 * time.Millisecond
	rec := &recorder{}
	cfg := s3api.Config{WriteIdleTimeout: idle, Observer: rec}
	base := seedObject(t, cfg, "stall", "obj", patterned(size))
	rec.reset()

	resp := getRaw(t, base, "/stall/obj")
	read, err := io.ReadFull(resp.Body, make([]byte, 4<<10))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // stall for several idle periods
	if _, err := io.Copy(io.Discard, resp.Body); err == nil {
		t.Fatal("stalled download was not cut")
	}
	_ = resp.Body.Close()

	got := rec.only(t)
	if got.op != "GetObject" || got.status != http.StatusOK {
		t.Fatalf("observed %+v, want GetObject 200", got)
	}
	if got.out < int64(read) || got.out >= size {
		t.Fatalf("bytesOut = %d, want between %d (read by the client) and %d (the object) exclusive", got.out, read, size)
	}
	if got.d < idle {
		t.Fatalf("duration = %v, want at least the idle timeout %v", got.d, idle)
	}
}

// TestObserverAuthFailure checks that a rejected signature is reported under
// the operation the router chose, with the status of the error.
func TestObserverAuthFailure(t *testing.T) {
	rec := &recorder{}
	authn := &fakes.Authenticator{Err: s3err.Errorf(s3err.SignatureDoesNotMatch, "The request signature does not match.")}
	base := startServerWith(t, s3api.Config{Observer: rec}, authn, nil, nil)
	status, body, err := doRaw(newHTTPClient(t, nil), http.MethodGet, base+"/bkt/k", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := rec.only(t)
	if status != http.StatusForbidden || got.op != "GetObject" || got.status != http.StatusForbidden || got.out != int64(len(body)) {
		t.Fatalf("status %d, observed %+v, body %q", status, got, body)
	}
}

// TestObserverMetrics checks that the metrics endpoint is reported as
// "Metrics", for a served scrape and for a refused bearer token.
func TestObserverMetrics(t *testing.T) {
	rec := &recorder{}
	base := startServerWith(t, s3api.Config{Metrics: metricsHook, MetricsToken: []byte(metricsToken), Observer: rec}, nil, nil, nil)
	hc := newHTTPClient(t, nil)

	status, body, err := doRaw(hc, http.MethodGet, base+"/unio/metrics", map[string]string{"Authorization": "Bearer " + metricsToken}, nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("scrape: status %d, err %v", status, err)
	}
	served := rec.waitCount(t, 1)[0]
	if served.op != "Metrics" || served.status != http.StatusOK || served.out != int64(len(body)) {
		t.Fatalf("scrape observed as %+v, want Metrics 200 with %d bytes out", served, len(body))
	}

	rec.reset()
	status, body, err = doRaw(hc, http.MethodGet, base+"/unio/metrics", map[string]string{"Authorization": "Bearer " + wrongSameLen}, nil)
	if err != nil || status != http.StatusForbidden {
		t.Fatalf("refused scrape: status %d, err %v", status, err)
	}
	refused := rec.only(t)
	if refused.op != "Metrics" || refused.status != http.StatusForbidden || refused.out != int64(len(body)) {
		t.Fatalf("refused scrape observed as %+v, want Metrics 403", refused)
	}
}

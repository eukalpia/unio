package s3api_test

import (
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/ut"

	"github.com/eukalpia/unio/internal/s3api"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// noopObserver is an Observer that discards every call.
type noopObserver struct{}

// ObserveHTTPRequest implements s3api.Observer and does nothing.
func (noopObserver) ObserveHTTPRequest(string, int, time.Duration, int64, int64) {}

// newEngineServer returns the request engine of a server with the fake layer
// and the given observer, which may be nil. The engine serves requests
// through Hertz's ut package, so no connection is needed.
func newEngineServer(t testing.TB, o s3api.Observer) *s3api.Server {
	t.Helper()
	srv, err := s3api.New(s3api.Config{Addr: "127.0.0.1:0", Observer: o}, fakes.NewLayer(), &fakes.Authenticator{}, &fakes.Authorizer{}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// allocsPerRequest returns the average allocations of one GET of path.
func allocsPerRequest(t *testing.T, o s3api.Observer, path string) float64 {
	t.Helper()
	engine := newEngineServer(t, o).Handler()
	return testing.AllocsPerRun(200, func() {
		ut.PerformRequest(engine, "GET", path, nil)
	})
}

// TestNoopObserverAddsNoAllocations checks that observing a request with an
// observer that does nothing allocates exactly as much as not observing it.
// The per-request record is pooled, so the bookkeeping must not allocate.
func TestNoopObserverAddsNoAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("sync.Pool drops items at random under the race detector")
	}
	for _, path := range []string{"/unio/health/live", "/b/missing"} {
		plain := allocsPerRequest(t, nil, path)
		observed := allocsPerRequest(t, noopObserver{}, path)
		if observed != plain {
			t.Fatalf("GET %s: %v allocations with a no-op observer, %v without", path, observed, plain)
		}
	}
}

// BenchmarkObserverOverhead reports the cost of one request through the
// engine, without an observer and with a no-op one. The allocations per
// operation must be the same in both.
func BenchmarkObserverOverhead(b *testing.B) {
	cases := []struct {
		name string
		o    s3api.Observer
	}{
		{"no observer", nil},
		{"no-op observer", noopObserver{}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			engine := newEngineServer(b, tc.o).Handler()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ut.PerformRequest(engine, "GET", "/unio/health/live", nil)
			}
		})
	}
}

package chaos

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitReady(t *testing.T) {
	tests := []struct {
		name      string
		failFirst int64 // number of initial probes answered 503
		timeout   time.Duration
		wantErr   error
	}{
		{name: "ready immediately", failFirst: 0, timeout: 5 * time.Second},
		{name: "ready after two failed probes", failFirst: 2, timeout: 5 * time.Second},
		{name: "never ready hits the deadline", failFirst: 1 << 30, timeout: 300 * time.Millisecond, wantErr: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var probes atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != readyPath {
					t.Errorf("probed %s, want %s", r.URL.Path, readyPath)
				}
				if probes.Add(1) <= tt.failFirst {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)

			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()
			err := WaitReady(ctx, srv.URL, 20*time.Millisecond)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("WaitReady: %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("WaitReady error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestWaitReadyUnreachableEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := WaitReady(ctx, url, 20*time.Millisecond); err == nil {
		t.Fatal("WaitReady succeeded against a closed endpoint")
	}
}

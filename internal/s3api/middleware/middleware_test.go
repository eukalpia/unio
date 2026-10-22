package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRequestIDGenerator(t *testing.T) {
	g := newRequestIDGen()
	seen := make(map[string]bool, 10000)
	for i := 0; i < 10000; i++ {
		id := g.next()
		if len(id) != 16 || strings.ToUpper(id) != id {
			t.Fatalf("id %q is not 16 upper-case hex characters", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestRequestIDHeaders(t *testing.T) {
	h := server.New()
	h.Use(RequestID("node-1"))
	h.GET("/x", func(ctx context.Context, c *app.RequestContext) { c.String(http.StatusOK, "ok") })
	w := ut.PerformRequest(h.Engine, "GET", "/x", nil)
	if w.Header().Get("x-amz-request-id") == "" || w.Header().Get("x-amz-id-2") != "node-1" {
		t.Fatalf("headers = %v", w.Header())
	}
}

func TestRecoverReturnsInternalError(t *testing.T) {
	h := server.New()
	h.Use(Recover(discard))
	h.GET("/boom", func(ctx context.Context, c *app.RequestContext) { panic("secret internal detail") })
	w := ut.PerformRequest(h.Engine, "GET", "/boom", nil)
	body := w.Body.String()
	if w.Result().StatusCode() != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Result().StatusCode())
	}
	if !strings.Contains(body, "<Code>InternalError</Code>") || strings.Contains(body, "secret internal detail") {
		t.Fatalf("body = %s", body)
	}
}

func TestLimiterRefusesWhenQueueFull(t *testing.T) {
	l, err := NewLimiter(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := l.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.acquire(ctx); err != errOverloaded {
		t.Fatalf("second acquire = %v, want errOverloaded", err)
	}
	l.release()
	if err := l.acquire(ctx); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	l.release()
}

func TestLimiterCancelledWaiter(t *testing.T) {
	l, _ := NewLimiter(1, 4)
	if err := l.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.acquire(ctx); err != context.Canceled {
		t.Fatalf("cancelled waiter got %v", err)
	}
	if l.queued.Load() != 0 {
		t.Fatalf("queue counter leaked: %d", l.queued.Load())
	}
	l.release()
}

func TestNewLimiterValidation(t *testing.T) {
	for _, c := range [][2]int{{0, 0}, {-1, 1}, {1, -1}} {
		if _, err := NewLimiter(c[0], c[1]); err == nil {
			t.Fatalf("NewLimiter(%d,%d) accepted", c[0], c[1])
		}
	}
}

func TestAdmissionSheds(t *testing.T) {
	l, _ := NewLimiter(1, 0)
	h := server.New()
	h.Use(RequestID("n"), Admission(l, discard))
	entered := make(chan struct{})
	release := make(chan struct{})
	h.GET("/slow", func(ctx context.Context, c *app.RequestContext) {
		close(entered)
		<-release
		c.String(http.StatusOK, "ok")
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ut.PerformRequest(h.Engine, "GET", "/slow", nil)
	}()
	<-entered
	w := ut.PerformRequest(h.Engine, "GET", "/slow", nil)
	close(release)
	wg.Wait()
	if w.Result().StatusCode() != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "SlowDown") {
		t.Fatalf("status %d body %s", w.Result().StatusCode(), w.Body.String())
	}
}

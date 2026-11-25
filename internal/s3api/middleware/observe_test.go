package middleware

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"

	"github.com/eukalpia/unio/internal/s3api/reqstat"
)

// report is one call of the function Observe reports to.
type report struct {
	op                string
	status            int
	bytesIn, bytesOut int64
	calls             int
}

// observed returns a report function that records into r.
func observed(r *report) func(string, int, time.Duration, int64, int64) {
	return func(op string, status int, _ time.Duration, in, out int64) {
		r.calls++
		r.op, r.status, r.bytesIn, r.bytesOut = op, status, in, out
	}
}

func TestObserveReportsOnceWithCounts(t *testing.T) {
	var got report
	h := server.New()
	h.Use(Observe(observed(&got)))
	h.PUT("/x", func(ctx context.Context, c *app.RequestContext) {
		st := reqstat.Get(c)
		st.SetOp("PutObject")
		st.AddBytesIn(12)
		c.String(http.StatusCreated, "hello")
	})
	ut.PerformRequest(h.Engine, "PUT", "/x", nil)
	want := report{op: "PutObject", status: http.StatusCreated, bytesIn: 12, bytesOut: 5, calls: 1}
	if got != want {
		t.Fatalf("report = %+v, want %+v", got, want)
	}
}

func TestObserveDefaultsToUnknown(t *testing.T) {
	var got report
	h := server.New()
	h.Use(Observe(observed(&got)))
	h.GET("/x", func(ctx context.Context, c *app.RequestContext) { c.Status(http.StatusNotFound) })
	ut.PerformRequest(h.Engine, "GET", "/x", nil)
	if got.op != reqstat.UnknownOp || got.status != http.StatusNotFound || got.calls != 1 {
		t.Fatalf("report = %+v", got)
	}
}

// TestObserveCountsStreamedBytes checks that a streamed body, which leaves the
// response body empty, is counted from the record.
func TestObserveCountsStreamedBytes(t *testing.T) {
	var got report
	h := server.New()
	h.Use(Observe(observed(&got)))
	h.GET("/x", func(ctx context.Context, c *app.RequestContext) {
		reqstat.Get(c).AddBytesStreamed(4096)
		c.Status(http.StatusOK)
	})
	ut.PerformRequest(h.Engine, "GET", "/x", nil)
	if got.bytesOut != 4096 || got.calls != 1 {
		t.Fatalf("report = %+v, want 4096 bytes out and one call", got)
	}
}

// TestObserveReportsAfterPanic checks that a handler panic, recovered by an
// inner middleware, is reported once with the status the client received.
func TestObserveReportsAfterPanic(t *testing.T) {
	var got report
	h := server.New()
	h.Use(Observe(observed(&got)), Recover(discard))
	h.GET("/x", func(ctx context.Context, c *app.RequestContext) { panic("boom") })
	ut.PerformRequest(h.Engine, "GET", "/x", nil)
	if got.status != http.StatusInternalServerError || got.calls != 1 {
		t.Fatalf("report = %+v, want one call with 500", got)
	}
}

// collect returns a report function that appends every call to reports.
func collect(reports *[]report) func(string, int, time.Duration, int64, int64) {
	return func(op string, status int, _ time.Duration, in, out int64) {
		*reports = append(*reports, report{op: op, status: status, bytesIn: in, bytesOut: out, calls: 1})
	}
}

// TestObserveReportsAdmissionRefusal fills the only admission slot and sends a
// second request, which is refused with 503 before any routing. The refusal
// is reported as an unrouted request, with its status and body length.
func TestObserveReportsAdmissionRefusal(t *testing.T) {
	limiter, err := NewLimiter(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	var reports []report
	h := server.New()
	h.Use(Observe(collect(&reports)), RequestID("node", discard), Admission(limiter, discard))
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
	refused := ut.PerformRequest(h.Engine, "GET", "/slow", nil)
	close(release)
	wg.Wait()

	if len(reports) != 2 {
		t.Fatalf("got %d reports, want one per request: %+v", len(reports), reports)
	}
	// The refusal finishes first, while the held request waits for release.
	if got, want := reports[0], (report{op: reqstat.UnknownOp, status: http.StatusServiceUnavailable, bytesOut: int64(refused.Body.Len()), calls: 1}); got != want {
		t.Fatalf("refusal reported as %+v, want %+v", got, want)
	}
	if reports[1].op != reqstat.UnknownOp || reports[1].status != http.StatusOK {
		t.Fatalf("held request reported as %+v, want the unknown op with 200", reports[1])
	}
}

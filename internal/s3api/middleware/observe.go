package middleware

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/s3api/reqstat"
)

// Observe reports every request to report once its response is complete.
//
// The report comes from a deferred call, so it happens exactly once even when
// an inner middleware panics. Observe must be the outermost middleware: the
// duration then covers the whole request, and the rejections of the inner
// middleware are reported too. A streamed GET is written by its handler, so
// its report comes after the last body byte. Any other response is complete
// once the chain has produced it, and the transport writes it right after.
//
// bytesOut is the streamed body count plus the length of the response body
// the chain left in place. Headers are not counted.
func Observe(report func(op string, status int, d time.Duration, bytesIn, bytesOut int64)) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		st := reqstat.Acquire()
		reqstat.Set(c, st)
		start := time.Now()
		defer func() {
			bytesOut := st.BytesStreamed + int64(len(c.Response.Body()))
			report(st.Op, c.Response.StatusCode(), time.Since(start), st.BytesIn, bytesOut)
			reqstat.Release(st)
		}()
		c.Next(ctx)
	}
}

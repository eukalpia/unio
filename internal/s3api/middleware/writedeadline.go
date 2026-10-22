package middleware

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

// WriteDeadline arms the write deadline of the connection with idle once the
// handler has returned. Hertz writes a response that is not streamed only
// after the handler chain completes, so the deadline covers that write. It is
// armed then rather than before the handler, because the handler may spend a
// long time reading the request body, and a deadline armed earlier would
// already have expired when the response is written. Streamed bodies arm the
// deadline themselves before each write.
func WriteDeadline(idle time.Duration) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		c.Next(ctx)
		if conn := c.GetConn(); conn != nil && idle > 0 {
			_ = conn.SetWriteTimeout(idle)
		}
	}
}

package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/s3api/reqinfo"
)

// AccessLog writes one structured line per request after it completes:
// method, path, status, response bytes, duration, client address and request
// id. The query string is never logged, because presigned URLs carry
// signatures there, and no header is logged, so credentials cannot leak.
func AccessLog(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		start := time.Now()
		c.Next(ctx)
		logger.Info("request",
			"request_id", reqinfo.Get(c).RequestID,
			"method", string(c.Method()),
			"path", string(c.Request.URI().PathOriginal()),
			"status", c.Response.StatusCode(),
			"bytes", c.Response.Header.ContentLength(),
			"duration", time.Since(start),
			"remote", c.RemoteAddr().String(),
		)
	}
}

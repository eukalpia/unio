package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/s3api/apierr"
	"github.com/eukalpia/unio/internal/s3err"
)

// Recover turns a panic in a later handler into a 500 InternalError S3
// response. The panic value and the stack are logged with the request id;
// neither reaches the client.
func Recover(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic while serving request", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				apierr.Write(c, logger, s3err.Wrap(s3err.InternalError, fmt.Errorf("panic: %v", r)), "")
				c.Abort()
			}
		}()
		c.Next(ctx)
	}
}

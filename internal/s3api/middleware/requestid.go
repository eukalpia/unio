// Package middleware holds the Hertz middleware of the S3 front end: request
// identifiers, panic recovery, admission control and the access log.
package middleware

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/s3api/apierr"
	"github.com/eukalpia/unio/internal/s3api/reqinfo"
	"github.com/eukalpia/unio/internal/s3err"
)

// requestIDBytes is the entropy of one request id: 64 random bits, rendered as
// 16 upper-case hexadecimal characters.
const requestIDBytes = 8

// hexUpper maps a nibble to its upper-case hexadecimal digit.
const hexUpper = "0123456789ABCDEF"

// newRequestID draws a request id from src. The ids are unpredictable and do
// not repeat across requests or restarts, so a client cannot guess the id of
// another request in the logs. An error from src is returned: the request then
// has no id, and it must not be served.
func newRequestID(src io.Reader) (string, error) {
	var raw [requestIDBytes]byte
	if _, err := io.ReadFull(src, raw[:]); err != nil {
		return "", fmt.Errorf("middleware: draw request id: %w", err)
	}
	var out [2 * requestIDBytes]byte
	for i, c := range raw {
		out[2*i] = hexUpper[c>>4]
		out[2*i+1] = hexUpper[c&0x0f]
	}
	return string(out[:]), nil
}

// RequestID assigns every request a request id drawn from crypto/rand and sets
// the x-amz-request-id and x-amz-id-2 response headers. hostID is the node
// identity reported in x-amz-id-2. A request whose id cannot be drawn is
// answered with 500 InternalError and does not reach the handlers.
func RequestID(hostID string, logger *slog.Logger) app.HandlerFunc {
	return requestIDMiddleware(hostID, logger, rand.Reader)
}

// requestIDMiddleware is RequestID with the entropy source injected, so that
// tests can exercise the failure path.
func requestIDMiddleware(hostID string, logger *slog.Logger, src io.Reader) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		id, err := newRequestID(src)
		if err != nil {
			apierr.Write(c, logger, s3err.Wrap(s3err.InternalError, err), "")
			c.Abort()
			return
		}
		info := reqinfo.Info{RequestID: id, HostID: hostID}
		reqinfo.Set(c, info)
		c.Response.Header.Set("x-amz-request-id", info.RequestID)
		c.Response.Header.Set("x-amz-id-2", info.HostID)
		c.Next(ctx)
	}
}

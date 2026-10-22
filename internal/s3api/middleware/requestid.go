// Package middleware holds the Hertz middleware of the S3 front end: request
// identifiers, panic recovery, admission control and the access log.
package middleware

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync/atomic"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/s3api/reqinfo"
)

// requestIDGen produces request ids. The sequence is multiplied by an odd
// constant, which is a bijection modulo 2^64, so the ids of one generator
// are distinct for its first 2^64 requests. A random per-generator epoch
// keeps ids from repeating across restarts. The ids are correlation handles,
// not secrets.
type requestIDGen struct {
	epoch uint64
	seq   atomic.Uint64
}

func newRequestIDGen() *requestIDGen {
	return &requestIDGen{epoch: rand.Uint64()}
}

// next returns a 16-character upper-case hexadecimal id.
func (g *requestIDGen) next() string {
	return fmt.Sprintf("%016X", g.epoch^(g.seq.Add(1)*0x9E3779B97F4A7C15))
}

// RequestID assigns every request a request id and sets the x-amz-request-id
// and x-amz-id-2 response headers. hostID is the node identity reported in
// x-amz-id-2.
func RequestID(hostID string) app.HandlerFunc {
	gen := newRequestIDGen()
	return func(ctx context.Context, c *app.RequestContext) {
		info := reqinfo.Info{RequestID: gen.next(), HostID: hostID}
		reqinfo.Set(c, info)
		c.Response.Header.Set("x-amz-request-id", info.RequestID)
		c.Response.Header.Set("x-amz-id-2", info.HostID)
		c.Next(ctx)
	}
}

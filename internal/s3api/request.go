package s3api

import (
	"io"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api/reqstat"
	"github.com/eukalpia/unio/internal/s3api/router"
)

// requestHeader copies the request headers into an http.Header with canonical
// keys, as the header parsers and the authenticator expect.
func requestHeader(c *app.RequestContext) http.Header {
	h := make(http.Header, c.Request.Header.Len())
	c.Request.Header.VisitAll(func(key, value []byte) {
		h.Add(string(key), string(value))
	})
	return h
}

// routeRequest builds the router input for c.
func routeRequest(c *app.RequestContext) router.Request {
	uri := c.Request.URI()
	return router.Request{
		Method:     string(c.Method()),
		Host:       string(c.Request.Host()),
		Path:       string(uri.PathOriginal()),
		Query:      string(uri.QueryString()),
		CopySource: len(c.Request.Header.Peek("X-Amz-Copy-Source")) > 0,
	}
}

// authRequest builds the authenticator input for c. body is the raw request
// body; the caller wraps it with idleBody so that every read is bounded.
func authRequest(c *app.RequestContext, header http.Header, body io.Reader) *auth.Request {
	uri := c.Request.URI()
	return &auth.Request{
		Method:        string(c.Method()),
		Host:          string(c.Request.Host()),
		RawPath:       string(uri.PathOriginal()),
		RawQuery:      string(uri.QueryString()),
		Header:        header,
		Body:          body,
		ContentLength: int64(c.Request.Header.ContentLength()),
	}
}

// idleBody bounds every read of a request body by an idle timeout: the
// connection read deadline is pushed forward before each read, so a client
// that sends nothing for longer than idle is disconnected, while a long but
// steadily flowing upload is not cut off. It also counts the bytes read into
// the request record, when there is one.
type idleBody struct {
	conn  interface{ SetReadTimeout(time.Duration) error }
	r     io.Reader
	idle  time.Duration
	stats *reqstat.Stats
}

// Read implements io.Reader.
func (b *idleBody) Read(p []byte) (int, error) {
	if b.idle > 0 && b.conn != nil {
		_ = b.conn.SetReadTimeout(b.idle)
	}
	n, err := b.r.Read(p)
	b.stats.AddBytesIn(n)
	return n, err
}

// requestBody returns the bounded request body reader for c, or nil when the
// request has no body stream. st receives the count of bytes read and may be
// nil.
func requestBody(c *app.RequestContext, idle time.Duration, st *reqstat.Stats) io.Reader {
	stream := c.Request.BodyStream()
	if stream == nil {
		return nil
	}
	return &idleBody{conn: c.GetConn(), r: stream, idle: idle, stats: st}
}

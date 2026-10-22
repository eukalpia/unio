package s3api

import (
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/s3api/apierr"
	"github.com/eukalpia/unio/internal/s3err"
)

// serveConsole answers a request under /unio/console/ from the console assets.
// The assets are public, so no signature is checked. A file is streamed from
// the binary, so a large bundle is not copied into a buffer for each request.
func (s *Server) serveConsole(c *app.RequestContext) {
	if s.cfg.Console == nil {
		apierr.Write(c, s.logger, s3err.Errorf(s3err.NoSuchKey, "the specified key does not exist"), string(c.Request.URI().PathOriginal()))
		return
	}
	a := s.cfg.Console.Open(string(c.Method()), string(c.Request.URI().PathOriginal()))
	for k, vs := range a.Header {
		for _, v := range vs {
			c.Response.Header.Set(k, v)
		}
	}
	c.SetStatusCode(a.Status)
	switch {
	case a.Body != nil:
		// The transport closes the body once it has been written.
		c.Response.SetBodyStream(a.Body, int(a.Size))
	case string(c.Method()) == http.MethodHead && a.Status == http.StatusOK:
		c.Response.Header.SetContentLength(int(a.Size))
	default:
		c.Response.Header.SetContentLength(0)
	}
}

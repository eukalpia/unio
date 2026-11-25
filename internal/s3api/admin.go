package s3api

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/admin"
	"github.com/eukalpia/unio/internal/s3api/apierr"
	"github.com/eukalpia/unio/internal/s3api/reqinfo"
	"github.com/eukalpia/unio/internal/s3err"
)

// serveAdmin hands a request under /unio/admin/v1/ to the admin service. The
// service authenticates the caller itself, so no S3 signature is checked here.
// The body is read through the same idle-bounded reader as an S3 body, and the
// service applies its own size limit.
func (s *Server) serveAdmin(ctx context.Context, c *app.RequestContext) {
	if s.cfg.Admin == nil {
		apierr.Write(c, s.logger, s3err.Errorf(s3err.NoSuchKey, "the specified key does not exist"), string(c.Request.URI().PathOriginal()))
		return
	}
	uri := c.Request.URI()
	resp := s.cfg.Admin.Serve(ctx, admin.Request{
		Method:        string(c.Method()),
		Path:          string(uri.PathOriginal()),
		Query:         string(uri.QueryString()),
		Host:          string(c.Request.Host()),
		Header:        requestHeader(c),
		Body:          requestBody(c, s.cfg.ReadBodyIdleTimeout, nil),
		ContentLength: int64(c.Request.Header.ContentLength()),
		RemoteAddr:    c.RemoteAddr().String(),
		RequestID:     reqinfo.Get(c).RequestID,
	})
	for k, vs := range resp.Header {
		for _, v := range vs {
			c.Response.Header.Add(k, v)
		}
	}
	c.SetStatusCode(resp.Status)
	if len(resp.Body) == 0 {
		c.Response.Header.SetContentLength(0)
		return
	}
	c.Response.SetBody(resp.Body)
}

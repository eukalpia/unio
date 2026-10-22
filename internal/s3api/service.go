package s3api

import (
	"bytes"
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api/apierr"
	"github.com/eukalpia/unio/internal/s3api/router"
	"github.com/eukalpia/unio/internal/s3err"
)

// serveService answers the endpoints of the reserved /unio/ namespace. Health
// probes are unauthenticated, as load balancers must reach them. Metrics
// require the admin action.
func (s *Server) serveService(ctx context.Context, c *app.RequestContext, rt router.Route) {
	switch rt.Op {
	case router.OpHealthLive:
		writeText(c, 200, "ok")
	case router.OpHealthReady:
		if s.cfg.Readiness != nil {
			if err := s.cfg.Readiness(ctx); err != nil {
				// The cause stays in the log; the probe answer is generic.
				s.logger.Warn("readiness check failed", "error", err.Error())
				writeText(c, 503, "unavailable")
				return
			}
		}
		writeText(c, 200, "ok")
	case router.OpMetrics:
		s.serveMetrics(ctx, c)
	}
}

// serveMetrics authenticates the caller, requires the admin action and then
// runs the configured metrics handler.
func (s *Server) serveMetrics(ctx context.Context, c *app.RequestContext) {
	resource := "/unio/metrics"
	if s.cfg.Metrics == nil {
		apierr.Write(c, s.logger, s3err.Errorf(s3err.NotImplemented, "metrics are not enabled on this node"), resource)
		return
	}
	header := requestHeader(c)
	res, err := s.authn.Authenticate(ctx, authRequest(c, header, nil))
	if err != nil {
		apierr.Write(c, s.logger, err, resource)
		return
	}
	if err := s.authz.Authorize(ctx, res.Identity, auth.ActionAdmin, "", ""); err != nil {
		apierr.Write(c, s.logger, err, resource)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resource, nil)
	if err != nil {
		apierr.Write(c, s.logger, s3err.Wrap(s3err.InternalError, err), resource)
		return
	}
	rec := newBufferedResponse()
	s.cfg.Metrics.ServeHTTP(rec, req)
	c.Data(rec.status, rec.header.Get("Content-Type"), rec.body.Bytes())
}

// writeText sends a small plain-text response. A HEAD request gets the status
// and no body.
func writeText(c *app.RequestContext, status int, text string) {
	if string(c.Method()) == http.MethodHead {
		c.SetStatusCode(status)
		c.Response.Header.SetContentLength(0)
		return
	}
	c.Data(status, "text/plain; charset=utf-8", []byte(text))
}

// bufferedResponse is an http.ResponseWriter that keeps the whole response in
// memory. It bridges a standard http.Handler, such as a metrics exporter, into
// the Hertz response. Metrics output is small, so buffering it is bounded.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

// newBufferedResponse returns an empty response that defaults to 200.
func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header), status: http.StatusOK}
}

// Header implements http.ResponseWriter.
func (b *bufferedResponse) Header() http.Header { return b.header }

// Write implements http.ResponseWriter.
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }

// WriteHeader implements http.ResponseWriter.
func (b *bufferedResponse) WriteHeader(status int) { b.status = status }

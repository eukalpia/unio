// Package s3api is the HTTP front end of Unio's S3 API. It routes requests,
// authenticates and authorizes them, dispatches each operation to its handler
// and renders the result as S3 XML. It serves on Hertz's standard transport,
// which supports TLS and streamed request bodies.
package s3api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/network/standard"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/apierr"
	"github.com/eukalpia/unio/internal/s3api/handlers"
	"github.com/eukalpia/unio/internal/s3api/middleware"
	"github.com/eukalpia/unio/internal/s3api/router"
)

// maxRequestBody is the transport body limit. Bodies are streamed, never
// buffered, so the transport does not need a size limit of its own: every
// operation checks its declared length against its S3 limit before it reads
// the body, and reports EntityTooLarge with the S3 code.
const maxRequestBody = math.MaxInt

// Server serves the S3 API. Create it with New, then call Run or Serve. A
// Server serves at most one listener at a time.
type Server struct {
	cfg     Config
	logger  *slog.Logger
	router  *router.Router
	env     *handlers.Env
	authn   auth.Authenticator
	authz   auth.Authorizer
	tls     *tls.Config
	hostID  string
	limiter *middleware.Limiter
	engine  *route.Engine

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	mu       sync.Mutex
	serving  bool
}

// New validates cfg and returns a Server backed by layer. authn verifies
// requests and authz decides on each action. A nil logger uses slog.Default.
func New(cfg Config, layer object.Layer, authn auth.Authenticator, authz auth.Authorizer, logger *slog.Logger) (*Server, error) {
	if layer == nil || authn == nil || authz == nil {
		return nil, errors.New("s3api: layer, authenticator and authorizer are required")
	}
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	tlsCfg, err := cfg.loadTLS()
	if err != nil {
		return nil, err
	}
	if tlsCfg == nil {
		logger.Warn("serving S3 over plain HTTP; configure a certificate for production use")
	}
	rt, err := router.New(cfg.Domains)
	if err != nil {
		return nil, fmt.Errorf("s3api: %w", err)
	}
	limiter, err := middleware.NewLimiter(cfg.MaxConcurrentRequests, cfg.MaxQueuedRequests)
	if err != nil {
		return nil, fmt.Errorf("s3api: %w", err)
	}
	hostID, err := newHostID()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:    cfg,
		logger: logger,
		router: rt,
		env: &handlers.Env{
			Layer:     layer,
			Region:    cfg.Region,
			Logger:    logger,
			CopyPool:  bufpool.NewPool(copyBufferSize, cfg.BufferBudget),
			WriteIdle: cfg.WriteIdleTimeout,
			WriteCap:  cfg.WriteTimeout,
		},
		authn:   authn,
		authz:   authz,
		tls:     tlsCfg,
		hostID:  hostID,
		limiter: limiter,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	s.engine = s.newHertz(nil).Engine
	return s, nil
}

// newHostID returns the x-amz-id-2 value identifying this server instance.
func newHostID() (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("s3api: generate host id: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

// Handler returns the request engine without a listener. It serves requests
// through Hertz's ut package and is meant for tests.
func (s *Server) Handler() *route.Engine {
	return s.engine
}

// Run listens on the configured address and serves until ctx is done, then
// shuts down gracefully within ShutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("s3api: listen on %s: %w", s.cfg.Addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx is done or Shutdown is called, then drains
// connections. It takes ownership of ln and closes it.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.mu.Lock()
	if s.serving {
		s.mu.Unlock()
		_ = ln.Close()
		return errors.New("s3api: server is already serving")
	}
	s.serving = true
	s.mu.Unlock()
	defer close(s.done)

	h := s.newHertz(deadlineListener{Listener: ln, headerTimeout: s.cfg.ReadHeaderTimeout})
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run() }()

	select {
	case err := <-errCh:
		_ = ln.Close()
		return err
	case <-ctx.Done():
	case <-s.stop:
	}
	// Closing the listener stops new connections even when Hertz has not
	// reached its running state yet; Shutdown then drains the active ones.
	_ = ln.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()
	if err := h.Shutdown(shutdownCtx); err != nil {
		s.logger.Warn("graceful shutdown did not complete", "error", err)
	}
	<-errCh
	return nil
}

// Shutdown stops a running Serve and waits for it to drain, bounded by ctx.
// It returns nil immediately when the server is not serving.
func (s *Server) Shutdown(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stop) })
	s.mu.Lock()
	serving := s.serving
	s.mu.Unlock()
	if !serving {
		return nil
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// newHertz builds the Hertz engine: the standard transport with streamed
// request bodies, the timeouts and limits of cfg, the middleware chain and
// the catch-all route. ln is nil for the test engine.
func (s *Server) newHertz(ln net.Listener) *server.Hertz {
	opts := []config.Option{
		server.WithTransport(standard.NewTransporter),
		server.WithStreamBody(true),
		server.WithMaxRequestBodySize(maxRequestBody),
		server.WithMaxHeaderBytes(object.MaxHeaderBytes),
		server.WithReadTimeout(s.cfg.ReadHeaderTimeout),
		server.WithIdleTimeout(s.cfg.IdleTimeout),
		server.WithExitWaitTime(s.cfg.ShutdownTimeout),
		server.WithDisablePrintRoute(true),
	}
	if ln != nil {
		opts = append(opts, server.WithListener(ln))
	}
	if s.tls != nil {
		opts = append(opts, server.WithTLS(s.tls))
	}
	h := server.New(opts...)
	h.Use(
		middleware.RequestID(s.hostID, s.logger),
		middleware.WriteDeadline(s.cfg.WriteIdleTimeout),
		middleware.AccessLog(s.logger),
		middleware.Recover(s.logger),
		middleware.Admission(s.limiter, s.logger),
	)
	h.Any("/", s.handle)
	h.Any("/*path", s.handle)
	return h
}

// handle serves one S3 request: route, authenticate, authorize, dispatch.
// Every failure is rendered as an S3 error by apierr.
func (s *Server) handle(ctx context.Context, c *app.RequestContext) {
	rt, err := s.router.Match(routeRequest(c))
	if err != nil {
		apierr.Write(c, s.logger, err, string(c.Request.URI().PathOriginal()))
		return
	}
	if rt.Op.IsService() {
		s.serveService(ctx, c, rt)
		return
	}
	resource := resourceOf(rt)

	header := requestHeader(c)
	res, err := s.authn.Authenticate(ctx, authRequest(c, header, requestBody(c, s.cfg.ReadBodyIdleTimeout)))
	if err != nil {
		apierr.Write(c, s.logger, err, resource)
		return
	}
	identity := res.Identity
	authorize := func(action auth.Action, bucket, key string) error {
		return s.authz.Authorize(ctx, identity, action, bucket, key)
	}
	if requiresPreAuthorization(rt.Op) {
		if err := authorize(primaryAction(rt.Op), rt.Bucket, rt.Key); err != nil {
			apierr.Write(c, s.logger, err, resource)
			return
		}
	}
	call := &handlers.Call{
		Ctx:       ctx,
		RC:        c,
		Route:     rt,
		Header:    header,
		Auth:      res,
		Authorize: authorize,
		Resource:  resource,
	}
	if err := s.env.Handle(rt.Op, call); err != nil {
		apierr.Write(c, s.logger, err, resource)
	}
}

// resourceOf names the bucket and key of a route for the Resource element.
func resourceOf(rt router.Route) string {
	switch {
	case rt.Bucket == "":
		return "/"
	case rt.Key == "":
		return "/" + rt.Bucket
	default:
		return "/" + rt.Bucket + "/" + rt.Key
	}
}

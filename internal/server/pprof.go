package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/obs"
)

// pprofTimeout bounds every phase of a profiling request. CPU profiles are
// taken for at most this long.
const pprofTimeout = 2 * time.Minute

// startPprof serves the profiling endpoints on their own loopback listener
// when enabled. They are never served on the S3 port, and they require the
// metrics bearer token, so profiling data, which can reveal memory contents,
// is reachable only from the node itself and with the token.
func startPprof(lc *lifecycle, cfg *config.Config, logger *slog.Logger) error {
	if !cfg.Metrics.Pprof {
		return nil
	}
	if !cfg.Metrics.Token.IsSet() {
		return errors.New("server: profiling requires a metrics token")
	}
	handler, err := obs.PprofHandler([]byte(cfg.Metrics.Token.Expose()))
	if err != nil {
		return fmt.Errorf("server: profiling: %w", err)
	}
	ln, err := net.Listen("tcp", cfg.Metrics.PprofAddress)
	if err != nil {
		return fmt.Errorf("server: profiling listener: %w", err)
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       pprofTimeout,
		WriteTimeout:      pprofTimeout + 10*time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("profiling server stopped", "error", err.Error())
		}
	}()
	lc.onStop("profiling server", func(ctx context.Context) error { return srv.Shutdown(ctx) })
	logger.Info("profiling enabled", "address", ln.Addr().String())
	return nil
}

package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/eukalpia/unio/internal/admin"
	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/console"
)

// adminTimeout bounds every phase of an admin or console request.
const adminTimeout = time.Minute

// startAdminListener serves the admin API and the console on their own
// address when node.admin_address is set. The S3 front end is then built
// without them, so the S3 port, which is often reachable from the internet
// through a proxy, never offers the sign-in page or the admin API.
func startAdminListener(lc *lifecycle, cfg *config.Config, adm *adminServices, logger *slog.Logger) error {
	if cfg.Node.AdminAddress == "" {
		return nil
	}
	ln, err := net.Listen("tcp", cfg.Node.AdminAddress)
	if err != nil {
		return fmt.Errorf("server: admin listener: %w", err)
	}
	srv := &http.Server{
		Handler:           adminHandler(adm.api, adm.console),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       adminTimeout,
		WriteTimeout:      adminTimeout,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("admin server stopped", "error", err.Error())
		}
	}()
	lc.onStop("admin server", func(ctx context.Context) error { return srv.Shutdown(ctx) })
	logger.Info("admin API and console served on their own address", "address", ln.Addr().String())
	return nil
}

// adminHandler routes /unio/admin/ to the admin service and /unio/console/
// to the console, and answers 404 for everything else.
func adminHandler(api *admin.Service, con *console.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		switch {
		case api != nil && strings.HasPrefix(path, admin.BasePath+"/"):
			resp := api.Serve(r.Context(), admin.Request{
				Method:        r.Method,
				Path:          path,
				Query:         r.URL.RawQuery,
				Host:          r.Host,
				Header:        r.Header,
				Body:          r.Body,
				ContentLength: r.ContentLength,
				RemoteAddr:    r.RemoteAddr,
				RequestID:     requestID(),
			})
			for k, vs := range resp.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.Status)
			_, _ = w.Write(resp.Body)
		case con != nil && (path == "/unio/console" || strings.HasPrefix(path, "/unio/console/")):
			a := con.Open(r.Method, path)
			for k, vs := range a.Header {
				for _, v := range vs {
					w.Header().Set(k, v)
				}
			}
			if a.Body == nil {
				w.WriteHeader(a.Status)
				return
			}
			defer func() {
				if c, ok := a.Body.(io.Closer); ok {
					_ = c.Close()
				}
			}()
			w.WriteHeader(a.Status)
			if r.Method != http.MethodHead {
				_, _ = io.Copy(w, a.Body)
			}
		default:
			http.NotFound(w, r)
		}
	})
}

// requestID returns 16 hex characters for the response and log lines.
func requestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return strings.ToUpper(hex.EncodeToString(b[:]))
}

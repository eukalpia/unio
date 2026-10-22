package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/eukalpia/unio/internal/admin"
	"github.com/eukalpia/unio/internal/auth/iam"
	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/drive/local"
	"github.com/eukalpia/unio/internal/obs"
	"github.com/eukalpia/unio/internal/s3api"
)

// frontEnd is the S3 endpoint and what it reports about the node.
type frontEnd struct {
	s3       *s3api.Server
	listener net.Listener
	metrics  *obs.Metrics
}

// startFront builds identity and access management on the metadata store and
// the S3 front end on the object engine, and binds the S3 listener. Requests
// are served only once Serve is called; until the metadata store has
// leaders, the readiness endpoint reports the node as not ready.
func startFront(lc *lifecycle, cfg *config.Config, cs *clusterServices, dp *dataPath, drives []*local.Drive, budget budgets, logger *slog.Logger) (*frontEnd, error) {
	secret := []byte(cfg.Cluster.Secret.Expose())
	access, err := iam.New(cs.store, iam.Config{
		KEK:           iam.DeriveKEK(secret),
		RootAccessKey: cfg.Auth.RootUser.Expose(),
		RootSecret:    cfg.Auth.RootPassword.Expose(),
		Region:        cfg.Node.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("server: identity and access management: %w", err)
	}

	metrics, err := obs.NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("server: metrics: %w", err)
	}
	recorder := admin.NewRecorder(nil)
	adm, err := startAdmin(cfg, access, cs, dp, budget, recorder, logger)
	if err != nil {
		return nil, err
	}
	health := obs.NewHealth(logger)
	health.AddReadinessCheck("metadata", cs.store.Ready)
	health.AddReadinessCheck("drives", drivesReady(cfg, drives))

	var token []byte
	if cfg.Metrics.Token.IsSet() {
		token = []byte(cfg.Metrics.Token.Expose())
	}
	s3, err := s3api.New(s3api.Config{
		Addr:                  cfg.Node.S3Address,
		TLSCertFile:           cfg.TLS.CertFile,
		TLSKeyFile:            cfg.TLS.KeyFile,
		RequireTLS:            cfg.TLS.Require,
		ReadHeaderTimeout:     cfg.Timeouts.ReadHeader,
		ReadBodyIdleTimeout:   cfg.Timeouts.ReadIdle,
		WriteIdleTimeout:      cfg.Timeouts.WriteIdle,
		WriteTimeout:          cfg.Timeouts.Write,
		IdleTimeout:           cfg.Timeouts.Idle,
		ShutdownTimeout:       cfg.Timeouts.Shutdown,
		MaxConcurrentRequests: cfg.Limits.MaxRequests,
		MaxQueuedRequests:     cfg.Limits.RequestQueue,
		Region:                cfg.Node.Region,
		Domains:               cfg.Node.Domains,
		Readiness:             health.Check,
		Metrics:               promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}),
		MetricsToken:          token,
		Observer:              observers{metrics, recorder},
		BufferBudget:          budget.front,
		Admin:                 adm.api,
		Console:               adm.console,
	}, dp.engine, access, access, logger.With("component", "s3api"))
	if err != nil {
		return nil, fmt.Errorf("server: s3 front end: %w", err)
	}
	ln, err := net.Listen("tcp", cfg.Node.S3Address)
	if err != nil {
		return nil, fmt.Errorf("server: s3 listener: %w", err)
	}
	// Serve owns the listener once it runs; closing it here covers a start
	// that fails before then, and is harmless after.
	lc.onClose("s3 listener", func() error {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	})
	return &frontEnd{s3: s3, listener: ln, metrics: metrics}, nil
}

// drivesReady reports the node ready for data while at least one of its
// configured drives is open. A node without local drives serves through its
// peers' drives and is always ready for data.
func drivesReady(cfg *config.Config, drives []*local.Drive) func(context.Context) error {
	configured := len(cfg.LocalDrives())
	online := len(drives)
	return func(context.Context) error {
		if configured > 0 && online == 0 {
			return fmt.Errorf("none of the %d local drives is online", configured)
		}
		return nil
	}
}

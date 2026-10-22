package server

import (
	"fmt"
	"log/slog"

	"github.com/eukalpia/unio/internal/admin"
	"github.com/eukalpia/unio/internal/auth/iam"
	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/console"
)

// adminServices are the admin API and the web console, served by the S3
// listener of the node.
type adminServices struct {
	api     *admin.Service
	console *console.Handler
}

// startAdmin builds the admin API over the node's identity store, metadata
// store, topology, object engine and buffer budget, and the console over the
// assets in the binary. It starts no goroutine: both are request-driven, and
// the statistics recorder is fed by the S3 front end.
func startAdmin(cfg *config.Config, access *iam.Store, cs *clusterServices, dp *dataPath, budget budgets, rec *admin.Recorder, logger *slog.Logger) (*adminServices, error) {
	api, err := admin.New(admin.Config{
		IAM:           access,
		Credentials:   access,
		Layer:         dp.engine,
		Cluster:       cs.topology,
		Deletions:     dp.engine,
		Recorder:      rec,
		BufferInUse:   budget.inUse,
		RootAccessKey: cfg.Auth.RootUser.Expose(),
		SessionSecret: []byte(cfg.Cluster.Secret.Expose()),
		Region:        cfg.Node.Region,
		Secure:        cfg.TLSEnabled(),
		Logger:        logger.With("component", "admin"),
	})
	if err != nil {
		return nil, fmt.Errorf("server: admin API: %w", err)
	}
	con, err := console.New()
	if err != nil {
		return nil, fmt.Errorf("server: console: %w", err)
	}
	return &adminServices{api: api, console: con}, nil
}

package main

import (
	"context"
	"log/slog"

	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/server"
)

// runServer starts the node with the validated configuration and blocks until
// ctx is cancelled, then drains requests and stops the node.
func runServer(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	return server.Run(ctx, cfg, logger)
}

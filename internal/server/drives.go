package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/cluster/topology"
	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/drive/local"
	"github.com/eukalpia/unio/internal/ids"
)

// openDrives formats and opens the drives of this node. Each drive is
// formatted with, or checked against, the id derived for its slot, so a drive
// moved to another slot or brought from another cluster is never served.
//
// Such an identity mismatch stops the start: it means the operator wired the
// drives differently from the configuration, and serving the drive would mix
// up shards. Any other failure leaves only that drive out: the erasure sets
// treat it as offline, reads reconstruct its shards from parity, and healing
// restores them once the drive is back.
func openDrives(ctx context.Context, lc *lifecycle, cfg *config.Config, id identity, budget *bufpool.Budget, logger *slog.Logger) ([]*local.Drive, error) {
	var drives []*local.Drive
	for _, ep := range cfg.LocalDrives() {
		driveID := topology.DriveIDFor(id.clusterID, id.node, ep.Path)
		d, err := openDrive(ctx, cfg, ep, id, driveID, budget)
		switch {
		case errors.Is(err, local.ErrClusterMismatch), errors.Is(err, local.ErrDriveIDMismatch):
			return nil, fmt.Errorf("server: drive %s belongs to another cluster or slot: %w", ep.Path, err)
		case err != nil:
			logger.Error("drive left offline", "drive", ep.Path, "error", err.Error())
			continue
		}
		lc.onClose("drive "+ep.Path, d.Close)
		drives = append(drives, d)
	}
	logger.Info("local drives opened", "online", len(drives), "configured", len(cfg.LocalDrives()))
	return drives, nil
}

// openDrive formats the drive at ep if it is new and opens it, checking its
// cluster and slot identity.
func openDrive(ctx context.Context, cfg *config.Config, ep config.Endpoint, id identity, driveID ids.DriveID, budget *bufpool.Budget) (*local.Drive, error) {
	if err := local.EnsureFormatted(ctx, ep.Path, id.clusterID, driveID); err != nil {
		return nil, err
	}
	return local.Open(ctx, ep.Path, local.Options{
		ClusterID:  id.clusterID,
		NodeID:     id.node,
		Endpoint:   ep.String(),
		ExpectedID: driveID,
		Budget:     budget,
		SyncPolicy: syncPolicy(cfg.Storage.Sync),
	})
}

// syncPolicy maps the configured sync mode to the drive option.
func syncPolicy(mode string) local.SyncPolicy {
	if mode == config.SyncBatch {
		return local.SyncBatch
	}
	return local.SyncAlways
}

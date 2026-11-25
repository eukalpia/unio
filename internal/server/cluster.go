package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/eukalpia/unio/internal/cluster/raftnet"
	"github.com/eukalpia/unio/internal/cluster/remotedrive"
	"github.com/eukalpia/unio/internal/cluster/topology"
	"github.com/eukalpia/unio/internal/cluster/uniwire"
	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/drive/local"
	"github.com/eukalpia/unio/internal/meta/raftstore"
)

// clusterServices are the components that connect this node to its peers.
type clusterServices struct {
	topology *topology.Topology
	store    *raftstore.Store
}

// startCluster registers every internode service on the transport, starts
// accepting peers, and opens the metadata store. The services are registered
// before the listener starts, so a peer never reaches a method that is not
// served yet; the store opens after it, so its Raft groups can elect leaders.
func startCluster(ctx context.Context, lc *lifecycle, cfg *config.Config, id identity, tr *uniwire.Transport, drives []*local.Drive, logger *slog.Logger) (*clusterServices, error) {
	raftNet, err := raftnet.New(tr, id.node, raftnet.Options{})
	if err != nil {
		return nil, fmt.Errorf("server: raft network: %w", err)
	}
	topo, err := topology.New(topology.Config{Local: id.node, Transport: tr, Map: id.clusterMap})
	if err != nil {
		return nil, fmt.Errorf("server: topology: %w", err)
	}
	drivesServer := remotedrive.NewServer(remotedrive.ServerOptions{})
	if err := drivesServer.Register(tr); err != nil {
		return nil, fmt.Errorf("server: remote drive service: %w", err)
	}
	lc.onClose("remote drive service", drivesServer.Close)
	for _, d := range drives {
		if err := topo.AddDrive(d); err != nil {
			return nil, fmt.Errorf("server: drive %x: %w", d.ID(), err)
		}
		if err := drivesServer.Add(d); err != nil {
			return nil, fmt.Errorf("server: serve drive %x: %w", d.ID(), err)
		}
	}

	addr, err := tr.Listen(cfg.Node.InternodeAddress)
	if err != nil {
		return nil, fmt.Errorf("server: internode listener: %w", err)
	}
	logger.Info("internode listener started", "address", addr.String())

	store, err := raftstore.Open(ctx, raftstore.Config{
		Node:    id.node,
		Map:     id.clusterMap,
		DataDir: cfg.MetadataDir(),
		RPC:     tr,
		Net:     raftNet,
		Options: raftstore.Options{Logger: logger.With("component", "meta")},
	})
	if err != nil {
		return nil, fmt.Errorf("server: metadata store: %w", err)
	}
	// Closing the store transfers the Raft leaderships this node holds, so
	// it runs while the transport is still up.
	lc.onClose("metadata store", store.Close)
	return &clusterServices{topology: topo, store: store}, nil
}

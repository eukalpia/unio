package server

import (
	"fmt"
	"net"
	"strconv"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/mapstore"
	"github.com/eukalpia/unio/internal/cluster/topology"
	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/ids"
)

// identity is what every node of the cluster derives identically from the
// shared configuration.
type identity struct {
	clusterID  string
	node       ids.NodeID
	clusterMap *cluster.Map
}

// resolveIdentity derives the cluster id and the cluster map from cfg, and
// reconciles the map with the one stored in the metadata directory. Every
// node computes the same map from the same endpoints and secret, so the Raft
// groups bootstrap consistently; a map that differs from the stored one means
// the layout of an existing cluster was changed, and the node refuses to
// start rather than join a different cluster.
func resolveIdentity(cfg *config.Config) (identity, error) {
	clusterID, err := topology.DeriveClusterID([]byte(cfg.Cluster.Secret.Expose()))
	if err != nil {
		return identity{}, fmt.Errorf("server: cluster id: %w", err)
	}
	spec, err := clusterSpec(cfg, clusterID)
	if err != nil {
		return identity{}, err
	}
	want, err := topology.MapFromSpec(spec)
	if err != nil {
		return identity{}, fmt.Errorf("server: cluster map: %w", err)
	}
	m, err := mapstore.Reconcile(cfg.MetadataDir(), want)
	if err != nil {
		return identity{}, fmt.Errorf("server: cluster map: %w", err)
	}
	return identity{clusterID: clusterID, node: cfg.NodeID(), clusterMap: m}, nil
}

// clusterSpec describes the cluster for MapFromSpec: one node per distinct
// endpoint host, with its drives in endpoint order. A node's S3 address uses
// its endpoint host and this node's S3 port, since the S3 port is the same on
// every node of a cluster.
func clusterSpec(cfg *config.Config, clusterID string) (topology.Spec, error) {
	setDrives, parity, err := cfg.ErasureLayout()
	if err != nil {
		return topology.Spec{}, fmt.Errorf("server: erasure layout: %w", err)
	}
	_, s3Port, err := net.SplitHostPort(cfg.Node.S3Address)
	if err != nil {
		return topology.Spec{}, fmt.Errorf("server: s3 address %q: %w", cfg.Node.S3Address, err)
	}
	spec := topology.Spec{
		ClusterID:  clusterID,
		SetSize:    setDrives,
		Parity:     parity,
		Partitions: cfg.Metadata.Partitions,
	}
	index := make(map[ids.NodeID]int)
	for _, ep := range cfg.Cluster.Drives {
		id, err := ids.NodeIDFromHost(ep.Host)
		if err != nil {
			return topology.Spec{}, fmt.Errorf("server: endpoint %s: %w", ep, err)
		}
		i, ok := index[id]
		if !ok {
			i = len(spec.Nodes)
			index[id] = i
			spec.Nodes = append(spec.Nodes, topology.NodeSpec{
				ID:     id,
				Addr:   net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port)),
				S3Addr: net.JoinHostPort(ep.Host, s3Port),
			})
		}
		spec.Nodes[i].Drives = append(spec.Nodes[i].Drives, ep.Path)
	}
	return spec, nil
}

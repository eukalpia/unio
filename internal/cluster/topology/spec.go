package topology

import (
	"fmt"
	"path/filepath"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

// NodeSpec describes one cluster member as configured.
type NodeSpec struct {
	// ID is the stable name of the node.
	ID ids.NodeID
	// Addr is the internode UniWire address, host:port.
	Addr string
	// S3Addr is the public S3 address, host:port.
	S3Addr string
	// Drives are the absolute drive paths on this node.
	Drives []string
}

// Spec is the cluster layout as configured. Every node reads the same Spec
// from the shared configuration, so every node builds the same cluster map.
type Spec struct {
	// ClusterID is the cluster id, normally DeriveClusterID of the secret.
	ClusterID string
	// Nodes are the cluster members. Their order does not matter.
	Nodes []NodeSpec
	// SetSize is the number of drives in each erasure set (K+M).
	SetSize int
	// Parity is the number of parity shards in each erasure set (M).
	Parity int
	// Partitions is the number of data partitions.
	Partitions int
}

// MapFromSpec builds the initial cluster map for s. It derives each drive id
// with DriveIDFor and lays the drives out with BuildInitialMap. The map
// depends only on the contents of s, not on the order of its nodes or drives,
// so nodes that read the same configuration build byte-identical maps.
//
// MapFromSpec rejects a node with no drives, a drive path that is not
// absolute, and a drive path that a node lists twice. Drive paths are stored
// cleaned. BuildInitialMap checks the rest: the cluster id, node ids and
// duplicate nodes, and the erasure set shape.
func MapFromSpec(s Spec) (*cluster.Map, error) {
	nodes := make([]cluster.NodeInfo, 0, len(s.Nodes))
	var drives []cluster.DriveLocation
	for _, ns := range s.Nodes {
		if len(ns.Drives) == 0 {
			return nil, fmt.Errorf("topology: node %s has no drives", ns.ID)
		}
		nodes = append(nodes, cluster.NodeInfo{ID: ns.ID, Addr: ns.Addr, S3Addr: ns.S3Addr})
		seen := make(map[string]bool, len(ns.Drives))
		for _, raw := range ns.Drives {
			p := filepath.Clean(raw)
			if !filepath.IsAbs(p) {
				return nil, fmt.Errorf("topology: node %s: drive path %q is not absolute", ns.ID, raw)
			}
			if seen[p] {
				return nil, fmt.Errorf("topology: node %s lists drive path %s more than once", ns.ID, p)
			}
			seen[p] = true
			drives = append(drives, cluster.DriveLocation{
				ID:   DriveIDFor(s.ClusterID, ns.ID, p),
				Node: ns.ID,
				Path: p,
			})
		}
	}
	return BuildInitialMap(s.ClusterID, nodes, drives, s.SetSize, s.Parity, s.Partitions)
}

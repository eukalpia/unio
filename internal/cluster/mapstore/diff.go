package mapstore

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

// describeDifference names the first field in which stored and want differ,
// in the order an operator reads a cluster map: identity, nodes, drives,
// erasure sets, then partitions. The result is a short clause for an error
// message.
func describeDifference(stored, want *cluster.Map) string {
	checks := []func(a, b *cluster.Map) string{
		diffClusterID,
		diffVersion,
		diffNodes,
		diffDrives,
		diffSets,
		diffPartitions,
	}
	for _, check := range checks {
		if d := check(stored, want); d != "" {
			return d
		}
	}
	return "the encodings differ in a field that this check does not name"
}

// diffClusterID compares the cluster ids.
func diffClusterID(a, b *cluster.Map) string {
	if a.ClusterID != b.ClusterID {
		return fmt.Sprintf("cluster id differs: stored has %q, configured has %q", a.ClusterID, b.ClusterID)
	}
	return ""
}

// diffVersion compares the map versions.
func diffVersion(a, b *cluster.Map) string {
	if a.Version != b.Version {
		return fmt.Sprintf("map version differs: stored has %d, configured has %d", a.Version, b.Version)
	}
	return ""
}

// diffNodes compares the node set, then the addresses of each node.
func diffNodes(a, b *cluster.Map) string {
	an, bn := nodesByID(a), nodesByID(b)
	if !sameNodeSet(an, bn) {
		return fmt.Sprintf("node set differs: stored has %s, configured has %s", nodeList(a), nodeList(b))
	}
	if len(a.Nodes) != len(b.Nodes) {
		return fmt.Sprintf("node count differs: stored has %d entries, configured has %d", len(a.Nodes), len(b.Nodes))
	}
	for _, id := range sortedNodeIDs(an) {
		x, y := an[id], bn[id]
		if x.Addr != y.Addr || x.S3Addr != y.S3Addr {
			return fmt.Sprintf("node %s address differs: stored has %s (S3 %s), configured has %s (S3 %s)",
				id, x.Addr, x.S3Addr, y.Addr, y.S3Addr)
		}
	}
	return ""
}

// diffDrives compares the drives by id, so that a drive which moved to
// another node or path is reported as that drive.
func diffDrives(a, b *cluster.Map) string {
	ad, bd := drivesByID(a), drivesByID(b)
	for _, id := range sortedDriveIDs(ad) {
		x := ad[id]
		y, ok := bd[id]
		switch {
		case !ok:
			return fmt.Sprintf("drive %s on node %s at %s is in the stored map only", id, x.Node, x.Path)
		case x.Node != y.Node || x.Path != y.Path:
			return fmt.Sprintf("drive %s is on node %s at %s in the stored map but on node %s at %s in the configured one",
				id, x.Node, x.Path, y.Node, y.Path)
		}
	}
	for _, id := range sortedDriveIDs(bd) {
		if _, ok := ad[id]; !ok {
			y := bd[id]
			return fmt.Sprintf("drive %s on node %s at %s is in the configured map only", id, y.Node, y.Path)
		}
	}
	return ""
}

// diffSets compares the erasure sets in order: their count, then each set's
// id and shape, then the drive in each slot.
func diffSets(a, b *cluster.Map) string {
	if len(a.Sets) != len(b.Sets) {
		return fmt.Sprintf("erasure set count differs: stored has %d, configured has %d", len(a.Sets), len(b.Sets))
	}
	for i := range a.Sets {
		x, y := a.Sets[i], b.Sets[i]
		switch {
		case x.ID != y.ID:
			return fmt.Sprintf("erasure set %d differs: configured has set %d in its place", x.ID, y.ID)
		case x.DataShards != y.DataShards || x.ParityShards != y.ParityShards:
			return fmt.Sprintf("erasure set %d differs: stored shape is %d+%d, configured is %d+%d",
				x.ID, x.DataShards, x.ParityShards, y.DataShards, y.ParityShards)
		case len(x.Drives) != len(y.Drives):
			return fmt.Sprintf("erasure set %d differs: stored has %d drives, configured has %d",
				x.ID, len(x.Drives), len(y.Drives))
		}
		for j := range x.Drives {
			if x.Drives[j] != y.Drives[j] {
				return fmt.Sprintf("erasure set %d differs: slot %d is drive %s in the stored map and %s in the configured one",
					x.ID, j, x.Drives[j], y.Drives[j])
			}
		}
	}
	return ""
}

// diffPartitions compares the partition count, then the system group and
// each data partition: its id, its replicas and its non-voting replicas.
func diffPartitions(a, b *cluster.Map) string {
	if len(a.Partitions) != len(b.Partitions) {
		return fmt.Sprintf("partition count differs: stored %d, configured %d", a.DataPartitions(), b.DataPartitions())
	}
	for i := range a.Partitions {
		x, y := a.Partitions[i], b.Partitions[i]
		name := fmt.Sprintf("partition %d", x.ID)
		if x.ID == ids.SystemPartition {
			name = "system group"
		}
		switch {
		case x.ID != y.ID:
			return fmt.Sprintf("partition %d differs: configured has partition %d in its place", x.ID, y.ID)
		case !slices.Equal(x.Replicas, y.Replicas):
			return fmt.Sprintf("%s differs: stored replicas are [%s], configured are [%s]",
				name, joinNodes(x.Replicas), joinNodes(y.Replicas))
		case !slices.Equal(x.NonVoters, y.NonVoters):
			return fmt.Sprintf("%s differs: stored non-voters are [%s], configured are [%s]",
				name, joinNodes(x.NonVoters), joinNodes(y.NonVoters))
		}
	}
	return ""
}

// nodesByID indexes the nodes of m by id.
func nodesByID(m *cluster.Map) map[ids.NodeID]cluster.NodeInfo {
	out := make(map[ids.NodeID]cluster.NodeInfo, len(m.Nodes))
	for _, n := range m.Nodes {
		out[n.ID] = n
	}
	return out
}

// drivesByID indexes the drives of m by id.
func drivesByID(m *cluster.Map) map[ids.DriveID]cluster.DriveLocation {
	out := make(map[ids.DriveID]cluster.DriveLocation, len(m.Drives))
	for _, d := range m.Drives {
		out[d.ID] = d
	}
	return out
}

// sameNodeSet reports whether a and b hold the same node ids.
func sameNodeSet(a, b map[ids.NodeID]cluster.NodeInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// sortedNodeIDs returns the node ids of m in ascending order.
func sortedNodeIDs(m map[ids.NodeID]cluster.NodeInfo) []ids.NodeID {
	out := make([]ids.NodeID, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// sortedDriveIDs returns the drive ids of m in byte order.
func sortedDriveIDs(m map[ids.DriveID]cluster.DriveLocation) []ids.DriveID {
	out := make([]ids.DriveID, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.SortFunc(out, func(x, y ids.DriveID) int {
		return bytes.Compare(x[:], y[:])
	})
	return out
}

// nodeList names the nodes of m in ascending order, for an error message.
func nodeList(m *cluster.Map) string {
	if len(m.Nodes) == 0 {
		return "no nodes"
	}
	names := make([]string, 0, len(m.Nodes))
	for _, n := range m.Nodes {
		names = append(names, string(n.ID))
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// joinNodes joins node ids with commas, for an error message.
func joinNodes(ns []ids.NodeID) string {
	names := make([]string, 0, len(ns))
	for _, n := range ns {
		names = append(names, string(n))
	}
	return strings.Join(names, ",")
}

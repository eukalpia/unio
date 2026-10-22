package topology

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/pki"
	"github.com/eukalpia/unio/internal/ids"
)

// Limits from the architecture (sections 3 and 6).
const (
	minSetSize   = 4
	maxSetSize   = 16
	replicaCount = 3
	maxVoters    = 5
)

// BuildInitialMap lays out a new cluster: erasure sets of drives and metadata
// partitions with their Raft replicas. The layout is a pure function of its
// inputs, so the result does not depend on their order.
//
// setSize is the number of drives in each erasure set (K+M) and parity is M.
// A set size of 1 with no parity is the single-drive development layout;
// otherwise setSize is in [4, 16] and parity is in [1, setSize/2]. The drive
// count must be a positive multiple of setSize, so that every drive joins a
// set. partitions is the number of data partitions (at least 1); the system
// group is always added as partition 0.
//
// Placement follows these rules:
//   - A set takes at most ceil(setSize/N) drives from any one node, where N is
//     the node count. Each set takes from the nodes that hold the most free
//     drives, so the drives of each node are used up evenly.
//   - Data partition k has min(3, N) replicas on consecutive nodes, starting at
//     node k mod N, so replicas are spread round-robin.
//   - The system group has min(5, N) voters, the first nodes by id. Any other
//     node holds a non-voting replica.
//
// If the drive spread cannot satisfy the per-node cap, BuildInitialMap fails
// rather than produce a layout that breaks it.
func BuildInitialMap(clusterID string, nodes []cluster.NodeInfo, drives []cluster.DriveLocation, setSize, parity, partitions int) (*cluster.Map, error) {
	if clusterID == "" {
		return nil, errors.New("topology: cluster id must not be empty")
	}
	if partitions < 1 {
		return nil, fmt.Errorf("topology: partitions must be at least 1, got %d", partitions)
	}
	if err := checkSetShape(setSize, parity); err != nil {
		return nil, err
	}
	sortedNodes, err := checkNodes(nodes)
	if err != nil {
		return nil, err
	}
	sortedDrives, err := checkDrives(sortedNodes, drives)
	if err != nil {
		return nil, err
	}
	if len(sortedDrives) == 0 || len(sortedDrives)%setSize != 0 {
		return nil, fmt.Errorf("topology: drive count %d must be a positive multiple of the set size %d", len(sortedDrives), setSize)
	}
	sets, err := placeSets(sortedNodes, sortedDrives, setSize)
	if err != nil {
		return nil, err
	}
	for i := range sets {
		sets[i].DataShards = setSize - parity
		sets[i].ParityShards = parity
	}
	return &cluster.Map{
		Version:    1,
		ClusterID:  clusterID,
		Nodes:      sortedNodes,
		Drives:     sortedDrives,
		Sets:       sets,
		Partitions: placePartitions(sortedNodes, partitions),
	}, nil
}

// checkSetShape validates the erasure set size and parity.
func checkSetShape(setSize, parity int) error {
	if setSize == 1 {
		if parity != 0 {
			return fmt.Errorf("topology: a single-drive set has no parity, got %d", parity)
		}
		return nil
	}
	if setSize < minSetSize || setSize > maxSetSize {
		return fmt.Errorf("topology: set size %d outside [%d, %d]", setSize, minSetSize, maxSetSize)
	}
	if parity < 1 || parity > setSize/2 {
		return fmt.Errorf("topology: parity %d must be in [1, %d]", parity, setSize/2)
	}
	return nil
}

// checkNodes validates the node list and returns a copy sorted by id.
func checkNodes(nodes []cluster.NodeInfo) ([]cluster.NodeInfo, error) {
	if len(nodes) == 0 {
		return nil, errors.New("topology: at least one node is required")
	}
	seen := make(map[ids.NodeID]bool, len(nodes))
	out := make([]cluster.NodeInfo, len(nodes))
	copy(out, nodes)
	for _, n := range out {
		if err := pki.ValidateNodeID(n.ID); err != nil {
			return nil, fmt.Errorf("topology: node %q: %w", n.ID, err)
		}
		if n.Addr == "" {
			return nil, fmt.Errorf("topology: node %s has no UniWire address", n.ID)
		}
		if seen[n.ID] {
			return nil, fmt.Errorf("topology: duplicate node %s", n.ID)
		}
		seen[n.ID] = true
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// checkDrives validates the drives against nodes and returns a copy sorted by
// id.
func checkDrives(nodes []cluster.NodeInfo, drives []cluster.DriveLocation) ([]cluster.DriveLocation, error) {
	known := make(map[ids.NodeID]bool, len(nodes))
	for _, n := range nodes {
		known[n.ID] = true
	}
	seen := make(map[ids.DriveID]bool, len(drives))
	out := make([]cluster.DriveLocation, len(drives))
	copy(out, drives)
	for _, d := range out {
		if d.ID.IsZero() {
			return nil, fmt.Errorf("topology: drive at %s has a zero id", d.Path)
		}
		if !known[d.Node] {
			return nil, fmt.Errorf("topology: drive %s is on unknown node %s", d.ID, d.Node)
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("topology: duplicate drive %s", d.ID)
		}
		seen[d.ID] = true
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].ID[:], out[j].ID[:]) < 0 })
	return out, nil
}

// placeSets groups the drives into erasure sets under the per-node cap. Each
// slot goes to the node with the most drives still free, which keeps the
// nodes balanced. Ties go to the earlier node.
func placeSets(nodes []cluster.NodeInfo, drives []cluster.DriveLocation, setSize int) ([]cluster.SetInfo, error) {
	n := len(nodes)
	perNodeCap := (setSize + n - 1) / n
	queues := make([][]ids.DriveID, n)
	index := make(map[ids.NodeID]int, n)
	for i, nd := range nodes {
		index[nd.ID] = i
	}
	for _, d := range drives {
		queues[index[d.Node]] = append(queues[index[d.Node]], d.ID)
	}

	sets := make([]cluster.SetInfo, 0, len(drives)/setSize)
	for s := range len(drives) / setSize {
		taken := make([]int, n)
		set := cluster.SetInfo{ID: ids.SetID(s), Drives: make([]ids.DriveID, 0, setSize)}
		for len(set.Drives) < setSize {
			best := -1
			for i := range n {
				if len(queues[i]) == 0 || taken[i] >= perNodeCap {
					continue
				}
				if best == -1 || len(queues[i]) > len(queues[best]) {
					best = i
				}
			}
			if best == -1 {
				return nil, fmt.Errorf("topology: cannot place set %d within %d drives per node: drives are too unevenly spread", s, perNodeCap)
			}
			set.Drives = append(set.Drives, queues[best][0])
			queues[best] = queues[best][1:]
			taken[best]++
		}
		sets = append(sets, set)
	}
	return sets, nil
}

// placePartitions assigns the system group and the data partitions to nodes.
// nodes must be sorted by id.
func placePartitions(nodes []cluster.NodeInfo, partitions int) []cluster.PartitionInfo {
	n := len(nodes)
	voters := min(n, maxVoters)
	system := cluster.PartitionInfo{ID: ids.SystemPartition}
	for i, nd := range nodes {
		if i < voters {
			system.Replicas = append(system.Replicas, nd.ID)
		} else {
			system.NonVoters = append(system.NonVoters, nd.ID)
		}
	}
	out := []cluster.PartitionInfo{system}

	replicas := min(replicaCount, n)
	for k := range partitions {
		info := cluster.PartitionInfo{ID: ids.PartitionID(k + 1)}
		for j := range replicas {
			info.Replicas = append(info.Replicas, nodes[(k+j)%n].ID)
		}
		out = append(out, info)
	}
	return out
}

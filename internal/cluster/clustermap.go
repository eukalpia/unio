package cluster

import "github.com/eukalpia/unio/internal/ids"

// NodeInfo describes a cluster member.
type NodeInfo struct {
	ID ids.NodeID
	// Addr is the UniWire internode address (host:port).
	Addr string
	// S3Addr is the public S3 endpoint (host:port).
	S3Addr string
}

// DriveLocation places a drive on a node.
type DriveLocation struct {
	ID   ids.DriveID
	Node ids.NodeID
	Path string
}

// SetInfo describes an erasure set.
type SetInfo struct {
	ID           ids.SetID
	DataShards   int
	ParityShards int
	// Drives lists the set's drives; shard i of a new write goes to Drives[i].
	Drives []ids.DriveID
}

// PartitionInfo describes a metadata partition and its Raft replicas.
type PartitionInfo struct {
	ID       ids.PartitionID
	Replicas []ids.NodeID
	// NonVoters receive the log but do not vote (system group only).
	NonVoters []ids.NodeID
}

// Map is an immutable, versioned description of the cluster. A new version
// is produced for every change; readers never see partial updates.
type Map struct {
	Version   uint64
	ClusterID string
	Nodes     []NodeInfo
	Drives    []DriveLocation
	Sets      []SetInfo
	// Partitions[0] is the system group; data partitions are 1..len-1.
	Partitions []PartitionInfo
}

// DataPartitions returns the number of data partitions.
func (m *Map) DataPartitions() int {
	if len(m.Partitions) == 0 {
		return 0
	}
	return len(m.Partitions) - 1
}

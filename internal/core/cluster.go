package core

import (
	"context"
	"encoding/binary"

	"github.com/hashicorp/raft"
	"github.com/zeebo/xxh3"
)

// NodeInfo describes a cluster member.
type NodeInfo struct {
	ID NodeID
	// Addr is the UniWire internode address (host:port).
	Addr string
	// S3Addr is the public S3 endpoint (host:port).
	S3Addr string
}

// DriveLocation places a drive on a node.
type DriveLocation struct {
	ID   DriveID
	Node NodeID
	Path string
}

// SetInfo describes an erasure set.
type SetInfo struct {
	ID           SetID
	DataShards   int
	ParityShards int
	// Drives lists the set's drives; shard i of a new write goes to Drives[i].
	Drives []DriveID
}

// PartitionInfo describes a metadata partition and its Raft replicas.
type PartitionInfo struct {
	ID       PartitionID
	Replicas []NodeID
	// NonVoters receive the log but do not vote (system group only).
	NonVoters []NodeID
}

// ClusterMap is an immutable, versioned description of the cluster. A new
// version is produced for every change; readers never see partial updates.
type ClusterMap struct {
	Version   uint64
	ClusterID string
	Nodes     []NodeInfo
	Drives    []DriveLocation
	Sets      []SetInfo
	// Partitions[0] is the system group; data partitions are 1..len-1.
	Partitions []PartitionInfo
}

// DataPartitions returns the number of data partitions.
func (m *ClusterMap) DataPartitions() int {
	if len(m.Partitions) == 0 {
		return 0
	}
	return len(m.Partitions) - 1
}

// PartitionOf maps an object key to its data partition in 1..partitions.
// Every node must use this function so that routing agrees cluster-wide.
func PartitionOf(bucket BucketID, key string, partitions int) PartitionID {
	if partitions <= 0 {
		return SystemPartition
	}
	var seed [8]byte
	binary.LittleEndian.PutUint64(seed[:], uint64(bucket))
	h := xxh3.HashSeed([]byte(key), xxh3.Hash(seed[:]))
	return PartitionID(h%uint64(partitions)) + 1
}

// Topology is the node's view of the cluster. Implemented by
// internal/cluster.
type Topology interface {
	// LocalNode returns this node's id.
	LocalNode() NodeID
	// Map returns the current cluster map. The result must not be modified.
	Map() *ClusterMap
	// Drive returns a Drive for id: the local storage engine for local
	// drives, a UniWire client for remote ones.
	Drive(id DriveID) (Drive, error)
	// Healthy reports whether a node currently answers health checks.
	Healthy(node NodeID) bool
}

// Message is a protobuf message with vtprotobuf fast paths.
type Message interface {
	MarshalVT() ([]byte, error)
	UnmarshalVT([]byte) error
}

// RPCHandler serves one RPC method. from is the authenticated caller.
type RPCHandler func(ctx context.Context, from NodeID, req []byte) (resp []byte, err error)

// RPC is request/response messaging between nodes over UniWire.
// Implemented by internal/cluster.
//
// Errors cross the wire faithfully: an *Error keeps its code and message,
// and the sentinel errors of this package (ErrNotLeader, ErrNotFound, ...)
// remain matchable with errors.Is on the caller side.
type RPC interface {
	// Call invokes method on node to and decodes the reply into resp.
	Call(ctx context.Context, to NodeID, method string, req, resp Message) error
	// Register installs the handler for method. Method names are
	// "<module>.<Name>", for example "meta.Apply".
	Register(method string, h RPCHandler) error
}

// RaftNetwork provides one raft.Transport per Raft group, multiplexed over
// UniWire. Raft server ids and addresses are NodeIDs. Implemented by
// internal/cluster.
type RaftNetwork interface {
	Transport(group PartitionID) (raft.Transport, error)
}

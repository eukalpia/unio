// Package cluster defines the node's view of the cluster: the versioned
// cluster Map, key-to-partition routing, the Topology that resolves drives,
// request/response RPC between nodes, and per-group Raft transports.
//
// Implementations live in subpackages: cluster/uniwire (mTLS transport),
// cluster/pki (trust bootstrap), cluster/remotedrive (drive.Drive over
// UniWire), cluster/raftnet (raft.Transport over UniWire), cluster/topology
// and cluster/mappb (protobuf conversion of the cluster map).
package cluster

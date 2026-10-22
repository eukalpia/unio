// Package raftnet carries hashicorp/raft traffic over UniWire.
//
// Network implements cluster.RaftNetwork. Each Raft group gets a raft.Transport,
// and every group shares the node's RPC methods, with the group named in each
// request. Votes, appends, pre-votes and timeout-now are RPCs. InstallSnapshot
// is a stream whose body is the snapshot. Raft server ids and addresses are node
// ids, resolved through the transport's resolver. Pipelined appends are not
// supported, so raft uses its single-request path. Heartbeats take the fast path
// through the heartbeat callback, as in hashicorp's own network transport.
package raftnet

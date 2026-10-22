package raftnet

//lint:file-ignore SA1019 Leader and Candidate are carried because hashicorp/raft 1.8 still reads them when Addr is empty; dropping them would make the wire mapping lossy.

import (
	"time"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Conversions between hashicorp/raft RPC types and their protobuf mirrors in
// wire.proto. Every conversion is lossless for the fields raft uses, so a
// round trip yields a value that raft treats as the original.

// headerToPB converts a raft RPC header.
func headerToPB(h raft.RPCHeader) *uniov1.RaftHeader {
	return &uniov1.RaftHeader{
		ProtocolVersion: uint32(h.ProtocolVersion),
		Id:              h.ID,
		Addr:            h.Addr,
	}
}

// headerFromPB converts a raft RPC header back.
func headerFromPB(h *uniov1.RaftHeader) raft.RPCHeader {
	return raft.RPCHeader{
		ProtocolVersion: raft.ProtocolVersion(h.GetProtocolVersion()),
		ID:              h.GetId(),
		Addr:            h.GetAddr(),
	}
}

// logToPB converts a log entry.
func logToPB(l *raft.Log) *uniov1.RaftLog {
	p := &uniov1.RaftLog{
		Index:      l.Index,
		Term:       l.Term,
		Type:       uint32(l.Type),
		Data:       l.Data,
		Extensions: l.Extensions,
	}
	if !l.AppendedAt.IsZero() {
		p.AppendedUnixNano = l.AppendedAt.UnixNano()
	}
	return p
}

// logFromPB converts a log entry back.
func logFromPB(p *uniov1.RaftLog) *raft.Log {
	l := &raft.Log{
		Index:      p.GetIndex(),
		Term:       p.GetTerm(),
		Type:       raft.LogType(p.GetType()),
		Data:       p.GetData(),
		Extensions: p.GetExtensions(),
	}
	if ns := p.GetAppendedUnixNano(); ns != 0 {
		l.AppendedAt = time.Unix(0, ns)
	}
	return l
}

// appendEntriesToPB wraps a raft AppendEntries request for group.
func appendEntriesToPB(group ids.PartitionID, r *raft.AppendEntriesRequest) *uniov1.RaftAppendEntries {
	p := &uniov1.RaftAppendEntries{
		Group:             uint32(group),
		Header:            headerToPB(r.RPCHeader),
		Term:              r.Term,
		Leader:            r.Leader,
		PrevLogEntry:      r.PrevLogEntry,
		PrevLogTerm:       r.PrevLogTerm,
		LeaderCommitIndex: r.LeaderCommitIndex,
	}
	for _, l := range r.Entries {
		p.Entries = append(p.Entries, logToPB(l))
	}
	return p
}

// appendEntriesFromPB converts a raft AppendEntries request back.
func appendEntriesFromPB(p *uniov1.RaftAppendEntries) *raft.AppendEntriesRequest {
	r := &raft.AppendEntriesRequest{
		RPCHeader:         headerFromPB(p.GetHeader()),
		Term:              p.GetTerm(),
		Leader:            p.GetLeader(),
		PrevLogEntry:      p.GetPrevLogEntry(),
		PrevLogTerm:       p.GetPrevLogTerm(),
		LeaderCommitIndex: p.GetLeaderCommitIndex(),
	}
	for _, l := range p.GetEntries() {
		r.Entries = append(r.Entries, logFromPB(l))
	}
	return r
}

// appendEntriesRespToPB converts a raft AppendEntries response.
func appendEntriesRespToPB(r *raft.AppendEntriesResponse) *uniov1.RaftAppendEntriesResponse {
	return &uniov1.RaftAppendEntriesResponse{
		Header:         headerToPB(r.RPCHeader),
		Term:           r.Term,
		LastLog:        r.LastLog,
		Success:        r.Success,
		NoRetryBackoff: r.NoRetryBackoff,
	}
}

// appendEntriesRespFromPB converts a raft AppendEntries response back.
func appendEntriesRespFromPB(p *uniov1.RaftAppendEntriesResponse) *raft.AppendEntriesResponse {
	return &raft.AppendEntriesResponse{
		RPCHeader:      headerFromPB(p.GetHeader()),
		Term:           p.GetTerm(),
		LastLog:        p.GetLastLog(),
		Success:        p.GetSuccess(),
		NoRetryBackoff: p.GetNoRetryBackoff(),
	}
}

// requestVoteToPB wraps a raft RequestVote request for group.
func requestVoteToPB(group ids.PartitionID, r *raft.RequestVoteRequest) *uniov1.RaftRequestVote {
	return &uniov1.RaftRequestVote{
		Group:              uint32(group),
		Header:             headerToPB(r.RPCHeader),
		Term:               r.Term,
		Candidate:          r.Candidate,
		LastLogIndex:       r.LastLogIndex,
		LastLogTerm:        r.LastLogTerm,
		LeadershipTransfer: r.LeadershipTransfer,
	}
}

// requestVoteFromPB converts a RequestVote request back.
func requestVoteFromPB(p *uniov1.RaftRequestVote) *raft.RequestVoteRequest {
	return &raft.RequestVoteRequest{
		RPCHeader:          headerFromPB(p.GetHeader()),
		Term:               p.GetTerm(),
		Candidate:          p.GetCandidate(),
		LastLogIndex:       p.GetLastLogIndex(),
		LastLogTerm:        p.GetLastLogTerm(),
		LeadershipTransfer: p.GetLeadershipTransfer(),
	}
}

// requestVoteRespToPB converts a RequestVote response.
func requestVoteRespToPB(r *raft.RequestVoteResponse) *uniov1.RaftRequestVoteResponse {
	return &uniov1.RaftRequestVoteResponse{
		Header:  headerToPB(r.RPCHeader),
		Term:    r.Term,
		Peers:   r.Peers,
		Granted: r.Granted,
	}
}

// requestVoteRespFromPB converts a RequestVote response back.
func requestVoteRespFromPB(p *uniov1.RaftRequestVoteResponse) *raft.RequestVoteResponse {
	return &raft.RequestVoteResponse{
		RPCHeader: headerFromPB(p.GetHeader()),
		Term:      p.GetTerm(),
		Peers:     p.GetPeers(),
		Granted:   p.GetGranted(),
	}
}

// preVoteToPB wraps a raft RequestPreVote request for group.
func preVoteToPB(group ids.PartitionID, r *raft.RequestPreVoteRequest) *uniov1.RaftRequestPreVote {
	return &uniov1.RaftRequestPreVote{
		Group:        uint32(group),
		Header:       headerToPB(r.RPCHeader),
		Term:         r.Term,
		LastLogIndex: r.LastLogIndex,
		LastLogTerm:  r.LastLogTerm,
	}
}

// preVoteFromPB converts a RequestPreVote request back.
func preVoteFromPB(p *uniov1.RaftRequestPreVote) *raft.RequestPreVoteRequest {
	return &raft.RequestPreVoteRequest{
		RPCHeader:    headerFromPB(p.GetHeader()),
		Term:         p.GetTerm(),
		LastLogIndex: p.GetLastLogIndex(),
		LastLogTerm:  p.GetLastLogTerm(),
	}
}

// preVoteRespToPB converts a RequestPreVote response.
func preVoteRespToPB(r *raft.RequestPreVoteResponse) *uniov1.RaftRequestPreVoteResponse {
	return &uniov1.RaftRequestPreVoteResponse{
		Header:  headerToPB(r.RPCHeader),
		Term:    r.Term,
		Granted: r.Granted,
	}
}

// preVoteRespFromPB converts a RequestPreVote response back.
func preVoteRespFromPB(p *uniov1.RaftRequestPreVoteResponse) *raft.RequestPreVoteResponse {
	return &raft.RequestPreVoteResponse{
		RPCHeader: headerFromPB(p.GetHeader()),
		Term:      p.GetTerm(),
		Granted:   p.GetGranted(),
	}
}

// installSnapshotToPB wraps a raft InstallSnapshot request for group. The
// snapshot bytes are not part of the message; they follow as the stream body.
func installSnapshotToPB(group ids.PartitionID, r *raft.InstallSnapshotRequest) *uniov1.RaftInstallSnapshot {
	return &uniov1.RaftInstallSnapshot{
		Group:              uint32(group),
		Header:             headerToPB(r.RPCHeader),
		SnapshotVersion:    uint32(r.SnapshotVersion),
		Term:               r.Term,
		Leader:             r.Leader,
		LastLogIndex:       r.LastLogIndex,
		LastLogTerm:        r.LastLogTerm,
		Peers:              r.Peers,
		Configuration:      r.Configuration,
		ConfigurationIndex: r.ConfigurationIndex,
		Size:               r.Size,
	}
}

// installSnapshotFromPB converts an InstallSnapshot request back.
func installSnapshotFromPB(p *uniov1.RaftInstallSnapshot) *raft.InstallSnapshotRequest {
	return &raft.InstallSnapshotRequest{
		RPCHeader:          headerFromPB(p.GetHeader()),
		SnapshotVersion:    raft.SnapshotVersion(p.GetSnapshotVersion()),
		Term:               p.GetTerm(),
		Leader:             p.GetLeader(),
		LastLogIndex:       p.GetLastLogIndex(),
		LastLogTerm:        p.GetLastLogTerm(),
		Peers:              p.GetPeers(),
		Configuration:      p.GetConfiguration(),
		ConfigurationIndex: p.GetConfigurationIndex(),
		Size:               p.GetSize(),
	}
}

// installSnapshotRespToPB converts an InstallSnapshot response.
func installSnapshotRespToPB(r *raft.InstallSnapshotResponse) *uniov1.RaftInstallSnapshotResponse {
	return &uniov1.RaftInstallSnapshotResponse{
		Header:  headerToPB(r.RPCHeader),
		Term:    r.Term,
		Success: r.Success,
	}
}

// installSnapshotRespFromPB converts an InstallSnapshot response back.
func installSnapshotRespFromPB(p *uniov1.RaftInstallSnapshotResponse) *raft.InstallSnapshotResponse {
	return &raft.InstallSnapshotResponse{
		RPCHeader: headerFromPB(p.GetHeader()),
		Term:      p.GetTerm(),
		Success:   p.GetSuccess(),
	}
}

// timeoutNowToPB wraps a raft TimeoutNow request for group.
func timeoutNowToPB(group ids.PartitionID, r *raft.TimeoutNowRequest) *uniov1.RaftTimeoutNow {
	return &uniov1.RaftTimeoutNow{Group: uint32(group), Header: headerToPB(r.RPCHeader)}
}

// timeoutNowFromPB converts a TimeoutNow request back.
func timeoutNowFromPB(p *uniov1.RaftTimeoutNow) *raft.TimeoutNowRequest {
	return &raft.TimeoutNowRequest{RPCHeader: headerFromPB(p.GetHeader())}
}

// timeoutNowRespToPB converts a TimeoutNow response.
func timeoutNowRespToPB(r *raft.TimeoutNowResponse) *uniov1.RaftTimeoutNowResponse {
	return &uniov1.RaftTimeoutNowResponse{Header: headerToPB(r.RPCHeader)}
}

// timeoutNowRespFromPB converts a TimeoutNow response back.
func timeoutNowRespFromPB(p *uniov1.RaftTimeoutNowResponse) *raft.TimeoutNowResponse {
	return &raft.TimeoutNowResponse{RPCHeader: headerFromPB(p.GetHeader())}
}

// isHeartbeat reports whether an AppendEntries request is a bare heartbeat.
// It uses the predicate of hashicorp/raft's network transport, so heartbeats
// take the same fast path here.
func isHeartbeat(r *raft.AppendEntriesRequest) bool {
	leader := r.Addr
	if len(leader) == 0 {
		leader = r.Leader
	}
	return r.Term != 0 && len(leader) > 0 &&
		r.PrevLogEntry == 0 && r.PrevLogTerm == 0 &&
		len(r.Entries) == 0 && r.LeaderCommitIndex == 0
}

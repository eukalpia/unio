package raftnet

//lint:file-ignore SA1019 Leader and Candidate are carried because hashicorp/raft 1.8 still reads them when Addr is empty; dropping them would make the wire mapping lossy.

import (
	"fmt"
	"math"
	"time"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Conversions between hashicorp/raft RPC types and their protobuf mirrors in
// wire.proto. Every conversion is lossless for the fields raft uses, so a
// round trip yields a value that raft treats as the original.
//
// Encoders refuse a value their wire field cannot hold, rather than truncating
// it. Decoders refuse a value outside what raft supports, rather than
// accepting a wrapped one. Both return an error, which callers surface as a
// failed call.

// protocolVersionToWire converts a raft protocol version to its wire field. It
// refuses versions raft does not support.
func protocolVersionToWire(v raft.ProtocolVersion) (uint32, error) {
	if v < raft.ProtocolVersionMin || v > raft.ProtocolVersionMax {
		return 0, fmt.Errorf("raftnet: protocol version %d is not supported", v)
	}
	return uint32(v), nil
}

// protocolVersionFromWire converts a wire protocol version back. It refuses
// versions raft does not support.
func protocolVersionFromWire(v uint32) (raft.ProtocolVersion, error) {
	if v > raft.ProtocolVersionMax {
		return 0, fmt.Errorf("raftnet: protocol version %d is not supported", v)
	}
	return raft.ProtocolVersion(v), nil
}

// snapshotVersionToWire converts a raft snapshot version to its wire field. It
// refuses versions raft does not support.
func snapshotVersionToWire(v raft.SnapshotVersion) (uint32, error) {
	if v < raft.SnapshotVersionMin || v > raft.SnapshotVersionMax {
		return 0, fmt.Errorf("raftnet: snapshot version %d is not supported", v)
	}
	return uint32(v), nil
}

// snapshotVersionFromWire converts a wire snapshot version back. It refuses
// versions raft does not support.
func snapshotVersionFromWire(v uint32) (raft.SnapshotVersion, error) {
	if v > raft.SnapshotVersionMax {
		return 0, fmt.Errorf("raftnet: snapshot version %d is not supported", v)
	}
	return raft.SnapshotVersion(v), nil
}

// logTypeFromWire converts a wire log type back. A log type is one byte in raft.
func logTypeFromWire(v uint32) (raft.LogType, error) {
	if v > math.MaxUint8 {
		return 0, fmt.Errorf("raftnet: log type %d does not fit a byte", v)
	}
	return raft.LogType(v), nil
}

// headerToPB converts a raft RPC header.
func headerToPB(h raft.RPCHeader) (*uniov1.RaftHeader, error) {
	pv, err := protocolVersionToWire(h.ProtocolVersion)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftHeader{
		ProtocolVersion: pv,
		Id:              h.ID,
		Addr:            h.Addr,
	}, nil
}

// headerFromPB converts a raft RPC header back.
func headerFromPB(h *uniov1.RaftHeader) (raft.RPCHeader, error) {
	pv, err := protocolVersionFromWire(h.GetProtocolVersion())
	if err != nil {
		return raft.RPCHeader{}, err
	}
	return raft.RPCHeader{
		ProtocolVersion: pv,
		ID:              h.GetId(),
		Addr:            h.GetAddr(),
	}, nil
}

// logToPB converts a log entry. Every log type fits the wire field.
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
func logFromPB(p *uniov1.RaftLog) (*raft.Log, error) {
	typ, err := logTypeFromWire(p.GetType())
	if err != nil {
		return nil, err
	}
	l := &raft.Log{
		Index:      p.GetIndex(),
		Term:       p.GetTerm(),
		Type:       typ,
		Data:       p.GetData(),
		Extensions: p.GetExtensions(),
	}
	if ns := p.GetAppendedUnixNano(); ns != 0 {
		l.AppendedAt = time.Unix(0, ns)
	}
	return l, nil
}

// appendEntriesToPB wraps a raft AppendEntries request for group.
func appendEntriesToPB(group ids.PartitionID, r *raft.AppendEntriesRequest) (*uniov1.RaftAppendEntries, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	p := &uniov1.RaftAppendEntries{
		Group:             uint32(group),
		Header:            h,
		Term:              r.Term,
		Leader:            r.Leader,
		PrevLogEntry:      r.PrevLogEntry,
		PrevLogTerm:       r.PrevLogTerm,
		LeaderCommitIndex: r.LeaderCommitIndex,
	}
	for _, l := range r.Entries {
		p.Entries = append(p.Entries, logToPB(l))
	}
	return p, nil
}

// appendEntriesFromPB converts a raft AppendEntries request back.
func appendEntriesFromPB(p *uniov1.RaftAppendEntries) (*raft.AppendEntriesRequest, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	r := &raft.AppendEntriesRequest{
		RPCHeader:         h,
		Term:              p.GetTerm(),
		Leader:            p.GetLeader(),
		PrevLogEntry:      p.GetPrevLogEntry(),
		PrevLogTerm:       p.GetPrevLogTerm(),
		LeaderCommitIndex: p.GetLeaderCommitIndex(),
	}
	for _, l := range p.GetEntries() {
		entry, err := logFromPB(l)
		if err != nil {
			return nil, err
		}
		r.Entries = append(r.Entries, entry)
	}
	return r, nil
}

// appendEntriesRespToPB converts a raft AppendEntries response.
func appendEntriesRespToPB(r *raft.AppendEntriesResponse) (*uniov1.RaftAppendEntriesResponse, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftAppendEntriesResponse{
		Header:         h,
		Term:           r.Term,
		LastLog:        r.LastLog,
		Success:        r.Success,
		NoRetryBackoff: r.NoRetryBackoff,
	}, nil
}

// appendEntriesRespFromPB converts a raft AppendEntries response back.
func appendEntriesRespFromPB(p *uniov1.RaftAppendEntriesResponse) (*raft.AppendEntriesResponse, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	return &raft.AppendEntriesResponse{
		RPCHeader:      h,
		Term:           p.GetTerm(),
		LastLog:        p.GetLastLog(),
		Success:        p.GetSuccess(),
		NoRetryBackoff: p.GetNoRetryBackoff(),
	}, nil
}

// requestVoteToPB wraps a raft RequestVote request for group.
func requestVoteToPB(group ids.PartitionID, r *raft.RequestVoteRequest) (*uniov1.RaftRequestVote, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftRequestVote{
		Group:              uint32(group),
		Header:             h,
		Term:               r.Term,
		Candidate:          r.Candidate,
		LastLogIndex:       r.LastLogIndex,
		LastLogTerm:        r.LastLogTerm,
		LeadershipTransfer: r.LeadershipTransfer,
	}, nil
}

// requestVoteFromPB converts a RequestVote request back.
func requestVoteFromPB(p *uniov1.RaftRequestVote) (*raft.RequestVoteRequest, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	return &raft.RequestVoteRequest{
		RPCHeader:          h,
		Term:               p.GetTerm(),
		Candidate:          p.GetCandidate(),
		LastLogIndex:       p.GetLastLogIndex(),
		LastLogTerm:        p.GetLastLogTerm(),
		LeadershipTransfer: p.GetLeadershipTransfer(),
	}, nil
}

// requestVoteRespToPB converts a RequestVote response.
func requestVoteRespToPB(r *raft.RequestVoteResponse) (*uniov1.RaftRequestVoteResponse, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftRequestVoteResponse{
		Header:  h,
		Term:    r.Term,
		Peers:   r.Peers,
		Granted: r.Granted,
	}, nil
}

// requestVoteRespFromPB converts a RequestVote response back.
func requestVoteRespFromPB(p *uniov1.RaftRequestVoteResponse) (*raft.RequestVoteResponse, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	return &raft.RequestVoteResponse{
		RPCHeader: h,
		Term:      p.GetTerm(),
		Peers:     p.GetPeers(),
		Granted:   p.GetGranted(),
	}, nil
}

// preVoteToPB wraps a raft RequestPreVote request for group.
func preVoteToPB(group ids.PartitionID, r *raft.RequestPreVoteRequest) (*uniov1.RaftRequestPreVote, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftRequestPreVote{
		Group:        uint32(group),
		Header:       h,
		Term:         r.Term,
		LastLogIndex: r.LastLogIndex,
		LastLogTerm:  r.LastLogTerm,
	}, nil
}

// preVoteFromPB converts a RequestPreVote request back.
func preVoteFromPB(p *uniov1.RaftRequestPreVote) (*raft.RequestPreVoteRequest, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	return &raft.RequestPreVoteRequest{
		RPCHeader:    h,
		Term:         p.GetTerm(),
		LastLogIndex: p.GetLastLogIndex(),
		LastLogTerm:  p.GetLastLogTerm(),
	}, nil
}

// preVoteRespToPB converts a RequestPreVote response.
func preVoteRespToPB(r *raft.RequestPreVoteResponse) (*uniov1.RaftRequestPreVoteResponse, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftRequestPreVoteResponse{
		Header:  h,
		Term:    r.Term,
		Granted: r.Granted,
	}, nil
}

// preVoteRespFromPB converts a RequestPreVote response back.
func preVoteRespFromPB(p *uniov1.RaftRequestPreVoteResponse) (*raft.RequestPreVoteResponse, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	return &raft.RequestPreVoteResponse{
		RPCHeader: h,
		Term:      p.GetTerm(),
		Granted:   p.GetGranted(),
	}, nil
}

// installSnapshotToPB wraps a raft InstallSnapshot request for group. The
// snapshot bytes are not part of the message; they follow as the stream body.
func installSnapshotToPB(group ids.PartitionID, r *raft.InstallSnapshotRequest) (*uniov1.RaftInstallSnapshot, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	sv, err := snapshotVersionToWire(r.SnapshotVersion)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftInstallSnapshot{
		Group:              uint32(group),
		Header:             h,
		SnapshotVersion:    sv,
		Term:               r.Term,
		Leader:             r.Leader,
		LastLogIndex:       r.LastLogIndex,
		LastLogTerm:        r.LastLogTerm,
		Peers:              r.Peers,
		Configuration:      r.Configuration,
		ConfigurationIndex: r.ConfigurationIndex,
		Size:               r.Size,
	}, nil
}

// installSnapshotFromPB converts an InstallSnapshot request back.
func installSnapshotFromPB(p *uniov1.RaftInstallSnapshot) (*raft.InstallSnapshotRequest, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	sv, err := snapshotVersionFromWire(p.GetSnapshotVersion())
	if err != nil {
		return nil, err
	}
	return &raft.InstallSnapshotRequest{
		RPCHeader:          h,
		SnapshotVersion:    sv,
		Term:               p.GetTerm(),
		Leader:             p.GetLeader(),
		LastLogIndex:       p.GetLastLogIndex(),
		LastLogTerm:        p.GetLastLogTerm(),
		Peers:              p.GetPeers(),
		Configuration:      p.GetConfiguration(),
		ConfigurationIndex: p.GetConfigurationIndex(),
		Size:               p.GetSize(),
	}, nil
}

// installSnapshotRespToPB converts an InstallSnapshot response.
func installSnapshotRespToPB(r *raft.InstallSnapshotResponse) (*uniov1.RaftInstallSnapshotResponse, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftInstallSnapshotResponse{
		Header:  h,
		Term:    r.Term,
		Success: r.Success,
	}, nil
}

// installSnapshotRespFromPB converts an InstallSnapshot response back.
func installSnapshotRespFromPB(p *uniov1.RaftInstallSnapshotResponse) (*raft.InstallSnapshotResponse, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	return &raft.InstallSnapshotResponse{
		RPCHeader: h,
		Term:      p.GetTerm(),
		Success:   p.GetSuccess(),
	}, nil
}

// timeoutNowToPB wraps a raft TimeoutNow request for group.
func timeoutNowToPB(group ids.PartitionID, r *raft.TimeoutNowRequest) (*uniov1.RaftTimeoutNow, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftTimeoutNow{Group: uint32(group), Header: h}, nil
}

// timeoutNowFromPB converts a TimeoutNow request back.
func timeoutNowFromPB(p *uniov1.RaftTimeoutNow) (*raft.TimeoutNowRequest, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	return &raft.TimeoutNowRequest{RPCHeader: h}, nil
}

// timeoutNowRespToPB converts a TimeoutNow response.
func timeoutNowRespToPB(r *raft.TimeoutNowResponse) (*uniov1.RaftTimeoutNowResponse, error) {
	h, err := headerToPB(r.RPCHeader)
	if err != nil {
		return nil, err
	}
	return &uniov1.RaftTimeoutNowResponse{Header: h}, nil
}

// timeoutNowRespFromPB converts a TimeoutNow response back.
func timeoutNowRespFromPB(p *uniov1.RaftTimeoutNowResponse) (*raft.TimeoutNowResponse, error) {
	h, err := headerFromPB(p.GetHeader())
	if err != nil {
		return nil, err
	}
	return &raft.TimeoutNowResponse{RPCHeader: h}, nil
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

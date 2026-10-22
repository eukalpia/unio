package raftnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/cluster/uniwire"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// errSnapshotAbandoned is what raft reads get once the handler that delivered
// a snapshot has returned.
var errSnapshotAbandoned = errors.New("raftnet: snapshot transfer abandoned")

// malformed is the error for a Raft request that does not decode.
func malformed() error {
	return s3err.Errorf(s3err.InvalidRequest, "malformed raft request")
}

// senderMismatch is the error for a request whose header names another node.
func senderMismatch() error {
	return s3err.Errorf(s3err.AccessDenied, "raft sender identity mismatch")
}

// deliver hands a request to the group's raft loop and waits for raft's
// answer. A heartbeat goes to the heartbeat callback, when one is set, so it
// does not queue behind disk work. The response channel has capacity one,
// so raft never blocks on an answer nobody reads.
func (n *Network) deliver(ctx context.Context, g *groupTransport, command any, body io.Reader, heartbeat bool) (any, error) {
	respCh := make(chan raft.RPCResponse, 1)
	rpc := raft.RPC{Command: command, Reader: body, RespChan: respCh}
	if cb := g.heartbeatHandler(); heartbeat && cb != nil {
		cb(rpc)
	} else {
		select {
		case g.consumer <- rpc:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	select {
	case r := <-respCh:
		if r.Error != nil {
			return nil, r.Error
		}
		return r.Response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// handleAppendEntries serves an AppendEntries RPC.
func (n *Network) handleAppendEntries(ctx context.Context, from ids.NodeID, req []byte) ([]byte, error) {
	var p uniov1.RaftAppendEntries
	if err := p.UnmarshalVT(req); err != nil {
		return nil, malformed()
	}
	if err := checkSender(from, p.GetHeader()); err != nil {
		return nil, senderMismatch()
	}
	g, err := n.groupFor(p.GetGroup())
	if err != nil {
		return nil, err
	}
	cmd, err := appendEntriesFromPB(&p)
	if err != nil {
		return nil, malformed()
	}
	out, err := n.deliver(ctx, g, cmd, nil, isHeartbeat(cmd))
	if err != nil {
		return nil, err
	}
	resp, ok := out.(*raft.AppendEntriesResponse)
	if !ok {
		return nil, fmt.Errorf("raftnet: append entries answered with %T", out)
	}
	msg, err := appendEntriesRespToPB(resp)
	if err != nil {
		return nil, err
	}
	return msg.MarshalVT()
}

// handleRequestVote serves a RequestVote RPC.
func (n *Network) handleRequestVote(ctx context.Context, from ids.NodeID, req []byte) ([]byte, error) {
	var p uniov1.RaftRequestVote
	if err := p.UnmarshalVT(req); err != nil {
		return nil, malformed()
	}
	if err := checkSender(from, p.GetHeader()); err != nil {
		return nil, senderMismatch()
	}
	g, err := n.groupFor(p.GetGroup())
	if err != nil {
		return nil, err
	}
	cmd, err := requestVoteFromPB(&p)
	if err != nil {
		return nil, malformed()
	}
	out, err := n.deliver(ctx, g, cmd, nil, false)
	if err != nil {
		return nil, err
	}
	resp, ok := out.(*raft.RequestVoteResponse)
	if !ok {
		return nil, fmt.Errorf("raftnet: request vote answered with %T", out)
	}
	msg, err := requestVoteRespToPB(resp)
	if err != nil {
		return nil, err
	}
	return msg.MarshalVT()
}

// handleRequestPreVote serves a RequestPreVote RPC.
func (n *Network) handleRequestPreVote(ctx context.Context, from ids.NodeID, req []byte) ([]byte, error) {
	var p uniov1.RaftRequestPreVote
	if err := p.UnmarshalVT(req); err != nil {
		return nil, malformed()
	}
	if err := checkSender(from, p.GetHeader()); err != nil {
		return nil, senderMismatch()
	}
	g, err := n.groupFor(p.GetGroup())
	if err != nil {
		return nil, err
	}
	cmd, err := preVoteFromPB(&p)
	if err != nil {
		return nil, malformed()
	}
	out, err := n.deliver(ctx, g, cmd, nil, false)
	if err != nil {
		return nil, err
	}
	resp, ok := out.(*raft.RequestPreVoteResponse)
	if !ok {
		return nil, fmt.Errorf("raftnet: pre-vote answered with %T", out)
	}
	msg, err := preVoteRespToPB(resp)
	if err != nil {
		return nil, err
	}
	return msg.MarshalVT()
}

// handleTimeoutNow serves a TimeoutNow RPC, which starts a leadership transfer.
func (n *Network) handleTimeoutNow(ctx context.Context, from ids.NodeID, req []byte) ([]byte, error) {
	var p uniov1.RaftTimeoutNow
	if err := p.UnmarshalVT(req); err != nil {
		return nil, malformed()
	}
	if err := checkSender(from, p.GetHeader()); err != nil {
		return nil, senderMismatch()
	}
	g, err := n.groupFor(p.GetGroup())
	if err != nil {
		return nil, err
	}
	cmd, err := timeoutNowFromPB(&p)
	if err != nil {
		return nil, malformed()
	}
	out, err := n.deliver(ctx, g, cmd, nil, false)
	if err != nil {
		return nil, err
	}
	resp, ok := out.(*raft.TimeoutNowResponse)
	if !ok {
		return nil, fmt.Errorf("raftnet: timeout-now answered with %T", out)
	}
	msg, err := timeoutNowRespToPB(resp)
	if err != nil {
		return nil, err
	}
	return msg.MarshalVT()
}

// handleInstallSnapshot serves an InstallSnapshot stream. The body is the
// snapshot; raft reads it while the handler waits for raft's answer.
func (n *Network) handleInstallSnapshot(ctx context.Context, req *uniwire.StreamRequest) (*uniwire.StreamResponse, error) {
	var p uniov1.RaftInstallSnapshot
	if err := p.UnmarshalVT(req.Payload); err != nil {
		return nil, malformed()
	}
	if err := checkSender(req.From, p.GetHeader()); err != nil {
		return nil, senderMismatch()
	}
	if req.BodyLen != p.GetSize() {
		return nil, s3err.Errorf(s3err.InvalidRequest, "snapshot length does not match its header")
	}
	g, err := n.groupFor(p.GetGroup())
	if err != nil {
		return nil, err
	}
	cmd, err := installSnapshotFromPB(&p)
	if err != nil {
		return nil, malformed()
	}
	// Raft may still hold the body when this handler gives up, and the
	// transport drains the body afterwards. The guard ensures that raft cannot
	// read after the handler returns, so the two never touch it concurrently.
	guard := &guardedReader{r: req.Body}
	defer guard.close()
	out, err := n.deliver(ctx, g, cmd, guard, false)
	if err != nil {
		return nil, err
	}
	resp, ok := out.(*raft.InstallSnapshotResponse)
	if !ok {
		return nil, fmt.Errorf("raftnet: install snapshot answered with %T", out)
	}
	msg, err := installSnapshotRespToPB(resp)
	if err != nil {
		return nil, err
	}
	payload, err := msg.MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("raftnet: encode snapshot reply: %w", err)
	}
	return &uniwire.StreamResponse{Payload: payload}, nil
}

// guardedReader lets raft read a snapshot body until its handler closes the
// guard. After close, every read fails without touching the underlying reader.
type guardedReader struct {
	mu     sync.Mutex
	r      io.Reader
	closed bool
}

// Read implements io.Reader.
func (g *guardedReader) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return 0, errSnapshotAbandoned
	}
	return g.r.Read(p)
}

// close marks the guard closed. It waits for any read in progress.
func (g *guardedReader) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
}

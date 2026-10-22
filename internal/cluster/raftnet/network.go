package raftnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/uniwire"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Method names of the Raft RPCs. InstallSnapshot is a stream because its body
// is the snapshot; the others are RPCs.
const (
	MethodAppendEntries   = "raft.AppendEntries"
	MethodRequestVote     = "raft.RequestVote"
	MethodRequestPreVote  = "raft.RequestPreVote"
	MethodTimeoutNow      = "raft.TimeoutNow"
	MethodInstallSnapshot = "raft.InstallSnapshot"
)

// consumerBuffer is the capacity of each group's inbound RPC channel.
const consumerBuffer = 64

// Transport is the part of a UniWire transport that a Network needs.
// *uniwire.Transport implements it.
type Transport interface {
	Register(method string, h cluster.Handler) error
	RegisterStream(method string, h uniwire.StreamHandler) error
	Call(ctx context.Context, to ids.NodeID, method string, req, resp cluster.Message) error
	Stream(ctx context.Context, to ids.NodeID, method string, payload []byte, body io.Reader, bodyLen int64) (uniwire.Reply, error)
}

// Options bounds the time spent on Raft RPCs.
type Options struct {
	// CallTimeout bounds a vote, append, pre-vote or timeout-now call.
	// Default two seconds.
	CallTimeout time.Duration
	// SnapshotTimeout bounds an InstallSnapshot transfer. Default two minutes.
	SnapshotTimeout time.Duration
}

// Network implements cluster.RaftNetwork over UniWire. It creates one
// raft.Transport per Raft group. All groups share the node's RPC methods, and
// each request names its group. Raft server ids and addresses are node ids.
// Network is safe for concurrent use.
type Network struct {
	tr              Transport
	local           ids.NodeID
	callTimeout     time.Duration
	snapshotTimeout time.Duration

	// mu guards groups.
	mu     sync.Mutex
	groups map[ids.PartitionID]*groupTransport
}

// Network implements cluster.RaftNetwork.
var _ cluster.RaftNetwork = (*Network)(nil)

// New returns a Network for the node local, registering the Raft methods on tr.
// It fails if tr already has them, which happens when two networks share a
// transport.
func New(tr Transport, local ids.NodeID, opts Options) (*Network, error) {
	if local == "" {
		return nil, errors.New("raftnet: empty local node id")
	}
	if opts.CallTimeout <= 0 {
		opts.CallTimeout = 2 * time.Second
	}
	if opts.SnapshotTimeout <= 0 {
		opts.SnapshotTimeout = 2 * time.Minute
	}
	n := &Network{
		tr:              tr,
		local:           local,
		callTimeout:     opts.CallTimeout,
		snapshotTimeout: opts.SnapshotTimeout,
		groups:          map[ids.PartitionID]*groupTransport{},
	}
	rpcs := map[string]cluster.Handler{
		MethodAppendEntries:  n.handleAppendEntries,
		MethodRequestVote:    n.handleRequestVote,
		MethodRequestPreVote: n.handleRequestPreVote,
		MethodTimeoutNow:     n.handleTimeoutNow,
	}
	for name, h := range rpcs {
		if err := tr.Register(name, h); err != nil {
			return nil, fmt.Errorf("raftnet: register %s: %w", name, err)
		}
	}
	if err := tr.RegisterStream(MethodInstallSnapshot, n.handleInstallSnapshot); err != nil {
		return nil, fmt.Errorf("raftnet: register %s: %w", MethodInstallSnapshot, err)
	}
	return n, nil
}

// Transport returns the raft.Transport of group, creating it on first use.
// Repeated calls return the same transport.
func (n *Network) Transport(group ids.PartitionID) (raft.Transport, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if g, ok := n.groups[group]; ok {
		return g, nil
	}
	g := &groupTransport{
		net:      n,
		group:    group,
		consumer: make(chan raft.RPC, consumerBuffer),
	}
	n.groups[group] = g
	return g, nil
}

// groupFor returns the transport of an inbound request's group. A group this
// node has not created cannot serve requests yet, so the sender retries.
func (n *Network) groupFor(group uint32) (*groupTransport, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	g, ok := n.groups[ids.PartitionID(group)]
	if !ok {
		return nil, fmt.Errorf("raftnet: group %d not hosted on %s", group, n.local)
	}
	return g, nil
}

// checkSender rejects a request whose header claims an identity other than the
// authenticated sender. Raft trusts the header, so this is the check that
// stops one node from voting or appending as another.
func checkSender(from ids.NodeID, h *uniov1.RaftHeader) error {
	if string(h.GetId()) != string(from) {
		return fmt.Errorf("raftnet: request from %s claims identity %q", from, h.GetId())
	}
	return nil
}

// groupTransport is the raft.Transport of one group.
type groupTransport struct {
	net      *Network
	group    ids.PartitionID
	consumer chan raft.RPC

	// hmu guards heartbeat.
	hmu       sync.Mutex
	heartbeat func(raft.RPC)
}

// Consumer implements raft.Transport.
func (g *groupTransport) Consumer() <-chan raft.RPC { return g.consumer }

// LocalAddr implements raft.Transport. The address is the node id.
func (g *groupTransport) LocalAddr() raft.ServerAddress {
	return raft.ServerAddress(g.net.local)
}

// AppendEntriesPipeline implements raft.Transport. Pipelining is not
// supported, so raft falls back to one request at a time.
func (g *groupTransport) AppendEntriesPipeline(raft.ServerID, raft.ServerAddress) (raft.AppendPipeline, error) {
	return nil, raft.ErrPipelineReplicationNotSupported
}

// AppendEntries implements raft.Transport.
func (g *groupTransport) AppendEntries(_ raft.ServerID, target raft.ServerAddress, args *raft.AppendEntriesRequest, resp *raft.AppendEntriesResponse) error {
	req, err := appendEntriesToPB(g.group, args)
	if err != nil {
		return err
	}
	var out uniov1.RaftAppendEntriesResponse
	if err := g.call(target, MethodAppendEntries, req, &out); err != nil {
		return err
	}
	decoded, err := appendEntriesRespFromPB(&out)
	if err != nil {
		return err
	}
	*resp = *decoded
	return nil
}

// RequestVote implements raft.Transport.
func (g *groupTransport) RequestVote(_ raft.ServerID, target raft.ServerAddress, args *raft.RequestVoteRequest, resp *raft.RequestVoteResponse) error {
	req, err := requestVoteToPB(g.group, args)
	if err != nil {
		return err
	}
	var out uniov1.RaftRequestVoteResponse
	if err := g.call(target, MethodRequestVote, req, &out); err != nil {
		return err
	}
	decoded, err := requestVoteRespFromPB(&out)
	if err != nil {
		return err
	}
	*resp = *decoded
	return nil
}

// RequestPreVote implements raft.WithPreVote.
func (g *groupTransport) RequestPreVote(_ raft.ServerID, target raft.ServerAddress, args *raft.RequestPreVoteRequest, resp *raft.RequestPreVoteResponse) error {
	req, err := preVoteToPB(g.group, args)
	if err != nil {
		return err
	}
	var out uniov1.RaftRequestPreVoteResponse
	if err := g.call(target, MethodRequestPreVote, req, &out); err != nil {
		return err
	}
	decoded, err := preVoteRespFromPB(&out)
	if err != nil {
		return err
	}
	*resp = *decoded
	return nil
}

// TimeoutNow implements raft.Transport.
func (g *groupTransport) TimeoutNow(_ raft.ServerID, target raft.ServerAddress, args *raft.TimeoutNowRequest, resp *raft.TimeoutNowResponse) error {
	req, err := timeoutNowToPB(g.group, args)
	if err != nil {
		return err
	}
	var out uniov1.RaftTimeoutNowResponse
	if err := g.call(target, MethodTimeoutNow, req, &out); err != nil {
		return err
	}
	decoded, err := timeoutNowRespFromPB(&out)
	if err != nil {
		return err
	}
	*resp = *decoded
	return nil
}

// InstallSnapshot implements raft.Transport. The snapshot streams from data as
// exactly args.Size bytes.
func (g *groupTransport) InstallSnapshot(_ raft.ServerID, target raft.ServerAddress, args *raft.InstallSnapshotRequest, resp *raft.InstallSnapshotResponse, data io.Reader) error {
	msg, err := installSnapshotToPB(g.group, args)
	if err != nil {
		return err
	}
	payload, err := msg.MarshalVT()
	if err != nil {
		return fmt.Errorf("raftnet: encode snapshot request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.net.snapshotTimeout)
	defer cancel()
	var body io.Reader
	if args.Size > 0 {
		body = data
	}
	rep, err := g.net.tr.Stream(ctx, ids.NodeID(target), MethodInstallSnapshot, payload, body, args.Size)
	if err != nil {
		return err
	}
	if rep.Body != nil {
		_ = rep.Body.Close()
	}
	var out uniov1.RaftInstallSnapshotResponse
	if err := out.UnmarshalVT(rep.Payload); err != nil {
		return fmt.Errorf("raftnet: decode snapshot reply: %w", err)
	}
	decoded, err := installSnapshotRespFromPB(&out)
	if err != nil {
		return err
	}
	*resp = *decoded
	return nil
}

// EncodePeer implements raft.Transport. The address is the node id itself.
func (g *groupTransport) EncodePeer(_ raft.ServerID, addr raft.ServerAddress) []byte {
	return []byte(addr)
}

// DecodePeer implements raft.Transport.
func (g *groupTransport) DecodePeer(b []byte) raft.ServerAddress {
	return raft.ServerAddress(b)
}

// SetHeartbeatHandler implements raft.Transport. Heartbeats bypass the
// consumer channel, so a busy raft loop cannot delay them.
func (g *groupTransport) SetHeartbeatHandler(cb func(rpc raft.RPC)) {
	g.hmu.Lock()
	defer g.hmu.Unlock()
	g.heartbeat = cb
}

// heartbeatHandler returns the current heartbeat callback, or nil.
func (g *groupTransport) heartbeatHandler() func(raft.RPC) {
	g.hmu.Lock()
	defer g.hmu.Unlock()
	return g.heartbeat
}

// call performs one RPC to target within the call timeout.
func (g *groupTransport) call(target raft.ServerAddress, method string, req, resp cluster.Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), g.net.callTimeout)
	defer cancel()
	return g.net.tr.Call(ctx, ids.NodeID(target), method, req, resp)
}

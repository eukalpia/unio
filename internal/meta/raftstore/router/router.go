// Package router sends metadata commands and reads to the leader of their
// partition. A replica that is not the leader forwards to the leader over
// cluster.RPC, and retries follow the leader hint with bounded backoff.
//
// Retry safety: a command is retried only after a node reports that it was
// not the leader, which happens before the command is appended. A transport
// failure during a write is not retried, because the command may already be
// in the log; the caller then sees group.ErrOutcomeUnknown. Reads have no side
// effects and are retried on any failure.
package router

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/group"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// RPC method names served by every node.
const (
	MethodApply = "meta.Apply"
	MethodRead  = "meta.Read"
)

// Retry policy: attempts are bounded, and each wait doubles from backoffMin
// up to backoffMax.
const (
	maxAttempts = 20
	backoffMin  = 5 * time.Millisecond
	backoffMax  = 250 * time.Millisecond
)

// ReplicasFunc returns the nodes that host a partition, voters first.
type ReplicasFunc func(p ids.PartitionID) []ids.NodeID

// Router routes requests to partition leaders. It is safe for concurrent use.
type Router struct {
	self     ids.NodeID
	rpc      cluster.RPC
	replicas ReplicasFunc

	mu     sync.RWMutex
	groups map[ids.PartitionID]*group.Group
	hints  map[ids.PartitionID]ids.NodeID
}

// New returns a router for node self and registers its RPC handlers.
func New(self ids.NodeID, rpc cluster.RPC, replicas ReplicasFunc) (*Router, error) {
	r := &Router{
		self:     self,
		rpc:      rpc,
		replicas: replicas,
		groups:   make(map[ids.PartitionID]*group.Group),
		hints:    make(map[ids.PartitionID]ids.NodeID),
	}
	if err := rpc.Register(MethodApply, r.handleApply); err != nil {
		return nil, fmt.Errorf("router: register %s: %w", MethodApply, err)
	}
	if err := rpc.Register(MethodRead, r.handleRead); err != nil {
		return nil, fmt.Errorf("router: register %s: %w", MethodRead, err)
	}
	return r, nil
}

// AddGroup registers the local replica of partition p.
func (r *Router) AddGroup(p ids.PartitionID, g *group.Group) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.groups[p] = g
}

// local returns the local replica of p, or nil when this node does not host
// it.
func (r *Router) local(p ids.PartitionID) *group.Group {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.groups[p]
}

// hint returns the last known leader of p.
func (r *Router) hint(p ids.PartitionID) (ids.NodeID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.hints[p]
	return h, ok
}

// setHint records the leader of p.
func (r *Router) setHint(p ids.PartitionID, n ids.NodeID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n == "" {
		delete(r.hints, p)
		return
	}
	r.hints[p] = n
}

// target picks the node to contact on the given attempt.
func (r *Router) target(p ids.PartitionID, attempt int) (ids.NodeID, bool) {
	if attempt == 0 {
		if h, ok := r.hint(p); ok {
			return h, true
		}
	}
	reps := r.replicas(p)
	if len(reps) == 0 {
		return "", false
	}
	return reps[attempt%len(reps)], true
}

// backoff returns the wait before attempt+1.
func backoff(attempt int) time.Duration {
	d := backoffMin << min(attempt, 16)
	if d <= 0 || d > backoffMax {
		return backoffMax
	}
	return d
}

// wait sleeps for d or until ctx is done.
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isApplicationError reports whether err carries an S3 code, which means the
// leader produced it (and it is therefore definitive), as opposed to a
// transport failure.
func isApplicationError(err error) bool {
	var c s3err.Coder
	return errors.As(err, &c)
}

// Propose replicates cmd in partition p and returns its result. It returns
// meta.ErrNotLeader only when the command was certainly not appended.
func (r *Router) Propose(ctx context.Context, p ids.PartitionID, cmd *pb.Command) (*pb.ApplyResult, error) {
	data, err := cmd.MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("router: encode command: %w", err)
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target, ok := r.target(p, attempt)
		if !ok {
			return nil, meta.ErrNotLeader
		}
		res, hint, notLeader, err := r.proposeTo(ctx, p, target, data)
		switch {
		case err != nil:
			return nil, err
		case !notLeader:
			r.setHint(p, target)
			return res, nil
		}
		r.setHint(p, hint)
		if err := wait(ctx, backoff(attempt)); err != nil {
			return nil, err
		}
	}
	return nil, meta.ErrNotLeader
}

// proposeTo sends data to node target. notLeader reports that the target did
// not append the command; hint names the leader it knows, if any.
func (r *Router) proposeTo(ctx context.Context, p ids.PartitionID, target ids.NodeID, data []byte) (res *pb.ApplyResult, hint ids.NodeID, notLeader bool, err error) {
	if target == r.self {
		if g := r.local(p); g != nil {
			res, err := g.Propose(ctx, data)
			if errors.Is(err, meta.ErrNotLeader) {
				leader, _ := g.Leader()
				return nil, leader, true, nil
			}
			return res, "", false, err
		}
	}
	req := &pb.ApplyRequest{Partition: uint32(p), Command: data}
	reply := &pb.ApplyReply{}
	if err := r.rpc.Call(ctx, target, MethodApply, req, reply); err != nil {
		if isApplicationError(err) {
			return nil, "", false, err
		}
		return nil, "", false, fmt.Errorf("%w: %v", group.ErrOutcomeUnknown, err)
	}
	if reply.NotLeader {
		return nil, ids.NodeID(reply.LeaderHint), true, nil
	}
	res = &pb.ApplyResult{}
	if err := res.UnmarshalVT(reply.Result); err != nil {
		return nil, "", false, fmt.Errorf("router: decode result: %w", err)
	}
	return res, "", false, nil
}

// Read serves a linearizable query of partition p from its leader.
func (r *Router) Read(ctx context.Context, p ids.PartitionID, q *pb.ReadQuery) (*pb.ReadResult, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target, ok := r.target(p, attempt)
		if !ok {
			return nil, meta.ErrNotLeader
		}
		res, hint, notLeader, err := r.readFrom(ctx, p, target, q)
		switch {
		case err != nil && isApplicationError(err):
			return nil, err
		case err != nil:
			lastErr = err
		case !notLeader:
			r.setHint(p, target)
			return res, nil
		default:
			lastErr = meta.ErrNotLeader
		}
		r.setHint(p, hint)
		if err := wait(ctx, backoff(attempt)); err != nil {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = meta.ErrNotLeader
	}
	return nil, lastErr
}

// readFrom sends a read to node target.
func (r *Router) readFrom(ctx context.Context, p ids.PartitionID, target ids.NodeID, q *pb.ReadQuery) (res *pb.ReadResult, hint ids.NodeID, notLeader bool, err error) {
	if target == r.self {
		if g := r.local(p); g != nil {
			res, err := g.Read(ctx, q)
			if errors.Is(err, meta.ErrNotLeader) {
				leader, _ := g.Leader()
				return nil, leader, true, nil
			}
			return res, "", false, err
		}
	}
	req := &pb.ReadRequest{Partition: uint32(p), Query: q}
	reply := &pb.ReadReply{}
	if err := r.rpc.Call(ctx, target, MethodRead, req, reply); err != nil {
		return nil, "", false, err
	}
	if reply.NotLeader {
		return nil, ids.NodeID(reply.LeaderHint), true, nil
	}
	res = &pb.ReadResult{}
	if err := res.UnmarshalVT(reply.Result); err != nil {
		return nil, "", false, fmt.Errorf("router: decode read result: %w", err)
	}
	return res, "", false, nil
}

// handleApply serves a forwarded command. A node that is not the leader
// replies NotLeader with its hint instead of forwarding again.
func (r *Router) handleApply(ctx context.Context, _ ids.NodeID, payload []byte) ([]byte, error) {
	var req pb.ApplyRequest
	if err := req.UnmarshalVT(payload); err != nil {
		return nil, fmt.Errorf("router: decode apply request: %w", err)
	}
	p := ids.PartitionID(req.Partition)
	reply := &pb.ApplyReply{}
	g := r.local(p)
	if g == nil {
		reply.NotLeader = true
		return reply.MarshalVT()
	}
	res, err := g.Propose(ctx, req.Command)
	if errors.Is(err, meta.ErrNotLeader) {
		leader, _ := g.Leader()
		reply.NotLeader = true
		reply.LeaderHint = string(leader)
		return reply.MarshalVT()
	}
	if err != nil {
		return nil, err
	}
	out, err := res.MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("router: encode result: %w", err)
	}
	reply.Result = out
	return reply.MarshalVT()
}

// handleRead serves a forwarded read. Like handleApply it never forwards.
func (r *Router) handleRead(ctx context.Context, _ ids.NodeID, payload []byte) ([]byte, error) {
	var req pb.ReadRequest
	if err := req.UnmarshalVT(payload); err != nil {
		return nil, fmt.Errorf("router: decode read request: %w", err)
	}
	p := ids.PartitionID(req.Partition)
	reply := &pb.ReadReply{}
	g := r.local(p)
	if g == nil {
		reply.NotLeader = true
		return reply.MarshalVT()
	}
	res, err := g.Read(ctx, req.Query)
	if errors.Is(err, meta.ErrNotLeader) {
		leader, _ := g.Leader()
		reply.NotLeader = true
		reply.LeaderHint = string(leader)
		return reply.MarshalVT()
	}
	if err != nil {
		return nil, err
	}
	out, err := res.MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("router: encode read result: %w", err)
	}
	reply.Result = out
	return reply.MarshalVT()
}

// Package group runs one Raft group: a hashicorp/raft instance bound to a
// state machine, with bootstrap from the cluster map, proposals that return
// the command's result, and linearizable reads.
package group

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/fsm"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Config describes one replica of a group.
type Config struct {
	Partition ids.PartitionID
	Local     ids.NodeID
	// Voters and NonVoters form the bootstrap configuration. They are
	// consulted only when the replica has no state yet.
	Voters    []ids.NodeID
	NonVoters []ids.NodeID

	Transport raft.Transport
	Logs      raft.LogStore
	Stable    raft.StableStore
	Snapshots raft.SnapshotStore
	FSM       *fsm.FSM

	// ApplyTimeout bounds how long a proposal waits to be queued.
	ApplyTimeout time.Duration
	// SnapshotInterval and SnapshotThreshold tune when Raft snapshots.
	SnapshotInterval  time.Duration
	SnapshotThreshold uint64
	// HeartbeatTimeout sets the follower and election timeouts, with the
	// leader lease at half of it. Zero keeps the hashicorp defaults.
	HeartbeatTimeout time.Duration
	// Log receives Raft's diagnostics. It may be nil.
	Log io.Writer
	// LeaderNotify, when set, receives true when this replica becomes leader
	// and false when it stops being leader. Raft blocks on it, so the owner
	// must drain it.
	LeaderNotify chan<- bool
}

// Group is a running replica of one Raft group.
type Group struct {
	id           ids.PartitionID
	raft         *raft.Raft
	fsm          *fsm.FSM
	applyTimeout time.Duration
	verify       *verifier
}

// Start creates the replica and bootstraps the group when the replica has
// no state yet. Every replica bootstraps from the same configuration, which
// comes from the cluster map, so concurrent bootstraps agree.
func Start(cfg Config) (*Group, error) {
	if cfg.ApplyTimeout <= 0 {
		return nil, errors.New("group: apply timeout must be positive")
	}
	conf := raft.DefaultConfig()
	conf.LocalID = raft.ServerID(cfg.Local)
	// Zero values keep the hashicorp defaults.
	if cfg.SnapshotInterval > 0 {
		conf.SnapshotInterval = cfg.SnapshotInterval
	}
	if cfg.SnapshotThreshold > 0 {
		conf.SnapshotThreshold = cfg.SnapshotThreshold
	}
	if cfg.HeartbeatTimeout > 0 {
		conf.HeartbeatTimeout = cfg.HeartbeatTimeout
		conf.ElectionTimeout = cfg.HeartbeatTimeout
		conf.LeaderLeaseTimeout = cfg.HeartbeatTimeout / 2
	}
	if cfg.Log != nil {
		conf.LogOutput = cfg.Log
	} else {
		conf.LogOutput = io.Discard
	}
	conf.NotifyCh = cfg.LeaderNotify
	has, err := raft.HasExistingState(cfg.Logs, cfg.Stable, cfg.Snapshots)
	if err != nil {
		return nil, fmt.Errorf("group %d: read state: %w", cfg.Partition, err)
	}
	if !has {
		bootstrap := bootstrapConfiguration(cfg)
		err := raft.BootstrapCluster(conf, cfg.Logs, cfg.Stable, cfg.Snapshots, cfg.Transport, bootstrap)
		if err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
			return nil, fmt.Errorf("group %d: bootstrap: %w", cfg.Partition, err)
		}
	}
	r, err := raft.NewRaft(conf, cfg.FSM, cfg.Logs, cfg.Stable, cfg.Snapshots, cfg.Transport)
	if err != nil {
		return nil, fmt.Errorf("group %d: start raft: %w", cfg.Partition, err)
	}
	return &Group{
		id:           cfg.Partition,
		raft:         r,
		fsm:          cfg.FSM,
		applyTimeout: cfg.ApplyTimeout,
		verify:       newVerifier(r),
	}, nil
}

// bootstrapConfiguration lists the voters and non-voters of the group.
func bootstrapConfiguration(cfg Config) raft.Configuration {
	var servers []raft.Server
	for _, n := range cfg.Voters {
		servers = append(servers, raft.Server{Suffrage: raft.Voter, ID: raft.ServerID(n), Address: raft.ServerAddress(n)})
	}
	for _, n := range cfg.NonVoters {
		servers = append(servers, raft.Server{Suffrage: raft.Nonvoter, ID: raft.ServerID(n), Address: raft.ServerAddress(n)})
	}
	return raft.Configuration{Servers: servers}
}

// ID returns the partition this group serves.
func (g *Group) ID() ids.PartitionID { return g.id }

// IsLeader reports whether this replica is currently the leader.
func (g *Group) IsLeader() bool { return g.raft.State() == raft.Leader }

// Leader returns the id of the leader as this replica knows it. ok is false
// when no leader is known.
func (g *Group) Leader() (leader ids.NodeID, ok bool) {
	_, id := g.raft.LeaderWithID()
	return ids.NodeID(id), id != ""
}

// Propose replicates an encoded fsm command and waits until it is applied.
//
// Failure modes that matter to callers:
//   - meta.ErrNotLeader: the command was not appended. It is safe to retry
//     at the leader.
//   - ErrOutcomeUnknown: the command may have been applied. Clients must
//     treat the write as possibly applied.
//   - An application error from the state machine: the command was rejected
//     and changed nothing.
func (g *Group) Propose(ctx context.Context, data []byte) (*pb.ApplyResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := g.applyTimeout
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem < timeout {
			timeout = rem
		}
	}
	fut := g.raft.Apply(data, timeout)
	done := make(chan error, 1)
	go func() { done <- fut.Error() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, mapApplyError(err)
		}
	case <-ctx.Done():
		// The command was appended, so its outcome is unknown until Raft
		// answers. The goroutine above finishes on its own.
		return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, ctx.Err())
	}
	out, ok := fut.Response().(*fsm.Outcome)
	if !ok || out == nil {
		return nil, fmt.Errorf("group %d: unexpected apply response %T", g.id, fut.Response())
	}
	if out.Err != nil {
		return nil, out.Err
	}
	if out.Result == nil {
		return &pb.ApplyResult{}, nil
	}
	return out.Result, nil
}

// mapApplyError classifies an error from Raft's Apply future.
func mapApplyError(err error) error {
	switch {
	case errors.Is(err, raft.ErrNotLeader):
		return meta.ErrNotLeader
	case errors.Is(err, raft.ErrEnqueueTimeout):
		return ErrBusy
	default:
		return fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
}

// Read answers a query linearizably. It must run on the leader: the leader
// confirms its leadership with a coalesced round, waits until the state
// machine has applied every entry committed before the read started, and
// then reads the state. Followers return meta.ErrNotLeader.
//
// The read path is the ReadIndex rule with a fast path. After the leader
// confirms its leadership, it reads its commit index c and its last log index.
// A leader's log holds every committed entry (leader completeness), so when
// c equals the last index, the log has nothing beyond c and c covers every
// committed write. When c is behind the last index, a newly elected leader
// may not yet have committed its own first entry, so the read first waits for
// a barrier. The state machine must also have applied up to c; otherwise the
// read waits for a barrier as well. A barrier is applied only after everything
// before it, which closes both gaps without polling.
func (g *Group) Read(ctx context.Context, q *pb.ReadQuery) (*pb.ReadResult, error) {
	if !g.IsLeader() {
		return nil, meta.ErrNotLeader
	}
	if err := g.verify.verify(ctx); err != nil {
		if errors.Is(err, raft.ErrNotLeader) {
			return nil, meta.ErrNotLeader
		}
		return nil, fmt.Errorf("group %d: verify leader: %w", g.id, err)
	}
	commit := g.raft.CommitIndex()
	last := g.raft.LastIndex()
	// Raft does not send no-op and configuration entries to the state
	// machine, so AppliedIndex can lag behind the commit index on an idle
	// leader; the barrier covers that case too.
	if commit < last || g.raft.AppliedIndex() < commit {
		if err := g.barrier(ctx); err != nil {
			return nil, err
		}
	}
	return g.fsm.Read(q)
}

// barrier waits until every entry appended before the call has been applied.
func (g *Group) barrier(ctx context.Context) error {
	fut := g.raft.Barrier(g.applyTimeout)
	done := make(chan error, 1)
	go func() { done <- fut.Error() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("group %d: barrier: %w", g.id, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the replica. It does not close the state machine, which the
// owner closes after the group has stopped.
func (g *Group) Close() error {
	if err := g.raft.Shutdown().Error(); err != nil {
		return fmt.Errorf("group %d: shutdown: %w", g.id, err)
	}
	return nil
}

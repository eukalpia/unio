package raftnet

import (
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/cluster/clustertest"
	"github.com/eukalpia/unio/internal/ids"
)

// countFSM is a raft state machine that counts applied commands. Its snapshot
// is the count, so restoring a snapshot must reproduce the count exactly.
type countFSM struct {
	mu       sync.Mutex
	applied  int
	restores int
}

// Apply implements raft.FSM.
func (f *countFSM) Apply(*raft.Log) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied++
	return f.applied
}

// Snapshot implements raft.FSM.
func (f *countFSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return countSnapshot(f.applied), nil
}

// Restore implements raft.FSM.
func (f *countFSM) Restore(rc io.ReadCloser) error {
	defer func() { _ = rc.Close() }()
	var n int
	if _, err := fmt.Fscan(rc, &n); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = n
	f.restores++
	return nil
}

// count returns the number of applied commands.
func (f *countFSM) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applied
}

// restoreCount returns how many snapshots were restored into the FSM.
func (f *countFSM) restoreCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restores
}

// countSnapshot is the snapshot of a countFSM.
type countSnapshot int

// Persist implements raft.FSMSnapshot.
func (s countSnapshot) Persist(sink raft.SnapshotSink) error {
	if _, err := fmt.Fprint(sink, int(s)); err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}

// Release implements raft.FSMSnapshot.
func (s countSnapshot) Release() {}

// raftNode is one member of a test Raft group.
type raftNode struct {
	id  ids.NodeID
	r   *raft.Raft
	fsm *countFSM
}

// testRaftConfig returns timings short enough for tests, with a tiny trailing
// log so that snapshots compact the log aggressively.
func testRaftConfig(id ids.NodeID) *raft.Config {
	conf := raft.DefaultConfig()
	conf.LocalID = raft.ServerID(id)
	conf.HeartbeatTimeout = 100 * time.Millisecond
	conf.ElectionTimeout = 100 * time.Millisecond
	conf.LeaderLeaseTimeout = 50 * time.Millisecond
	conf.CommitTimeout = 5 * time.Millisecond
	conf.TrailingLogs = 1
	conf.LogOutput = io.Discard
	return conf
}

// startRaftNode starts a Raft member on the transport of node id. The member
// is shut down when the test ends. Bootstrap, when given, seeds the
// configuration.
func startRaftNode(t *testing.T, c *clustertest.Cluster, id ids.NodeID, bootstrap []ids.NodeID) *raftNode {
	t.Helper()
	net, err := New(c.Nodes[id], id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	trans, err := net.Transport(ids.SystemPartition)
	if err != nil {
		t.Fatal(err)
	}
	conf := testRaftConfig(id)
	logs := raft.NewInmemStore()
	snaps := raft.NewInmemSnapshotStore()
	if len(bootstrap) > 0 {
		var servers []raft.Server
		for _, m := range bootstrap {
			servers = append(servers, raft.Server{Suffrage: raft.Voter, ID: raft.ServerID(m), Address: raft.ServerAddress(m)})
		}
		if err := raft.BootstrapCluster(conf, logs, logs, snaps, trans, raft.Configuration{Servers: servers}); err != nil {
			t.Fatalf("bootstrap %s: %v", id, err)
		}
	}
	fsm := &countFSM{}
	r, err := raft.NewRaft(conf, fsm, logs, logs, snaps, trans)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown().Error() })
	return &raftNode{id: id, r: r, fsm: fsm}
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// leaderAmong returns a member that currently reports itself leader.
func leaderAmong(nodes []*raftNode) *raftNode {
	for _, n := range nodes {
		if n.r.State() == raft.Leader {
			return n
		}
	}
	return nil
}

// waitLeader waits until one of nodes is leader and returns it.
func waitLeader(t *testing.T, nodes []*raftNode) *raftNode {
	t.Helper()
	var leader *raftNode
	waitFor(t, 15*time.Second, "a leader", func() bool {
		leader = leaderAmong(nodes)
		return leader != nil
	})
	return leader
}

// applyOne submits one command through leader and waits for it to commit.
func applyOne(t *testing.T, leader *raftNode) {
	t.Helper()
	if err := leader.r.Apply([]byte("cmd"), 10*time.Second).Error(); err != nil {
		t.Fatalf("apply on %s: %v", leader.id, err)
	}
}

// waitApplied waits until every node has applied n commands.
func waitApplied(t *testing.T, nodes []*raftNode, n int) {
	t.Helper()
	for _, node := range nodes {
		waitFor(t, 15*time.Second, fmt.Sprintf("%s to apply %d commands", node.id, n), func() bool {
			return node.fsm.count() >= n
		})
	}
}

func TestThreeNodesElectAndReplicate(t *testing.T) {
	members := []ids.NodeID{"n1", "n2", "n3"}
	c := clustertest.NewCluster(t, members...)
	var nodes []*raftNode
	for _, m := range members {
		nodes = append(nodes, startRaftNode(t, c, m, members))
	}
	leader := waitLeader(t, nodes)
	const commands = 20
	for range commands {
		applyOne(t, leader)
	}
	waitApplied(t, nodes, commands)
}

func TestLeadershipTransferUsesTimeoutNow(t *testing.T) {
	members := []ids.NodeID{"n1", "n2", "n3"}
	c := clustertest.NewCluster(t, members...)
	var nodes []*raftNode
	for _, m := range members {
		nodes = append(nodes, startRaftNode(t, c, m, members))
	}
	old := waitLeader(t, nodes)
	applyOne(t, old)
	if err := old.r.LeadershipTransfer().Error(); err != nil {
		t.Fatalf("leadership transfer: %v", err)
	}
	waitFor(t, 15*time.Second, "a different leader", func() bool {
		l := leaderAmong(nodes)
		return l != nil && l != old
	})
	newLeader := waitLeader(t, nodes)
	applyOne(t, newLeader)
	waitApplied(t, nodes, 2)
}

func TestLaggingNodeInstallsSnapshot(t *testing.T) {
	c := clustertest.NewCluster(t, "n1", "n2", "n3")
	// n1 and n2 form the group. n3 joins later, after the log is compacted,
	// so the leader must send it a snapshot rather than log entries.
	founding := []ids.NodeID{"n1", "n2"}
	var nodes []*raftNode
	for _, m := range founding {
		nodes = append(nodes, startRaftNode(t, c, m, founding))
	}
	leader := waitLeader(t, nodes)
	const before = 30
	for range before {
		applyOne(t, leader)
	}
	waitApplied(t, nodes, before)
	if err := leader.r.Snapshot().Error(); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	joiner := startRaftNode(t, c, "n3", nil)
	if err := leader.r.AddVoter("n3", "n3", 0, 15*time.Second).Error(); err != nil {
		t.Fatalf("add voter: %v", err)
	}
	waitApplied(t, []*raftNode{joiner}, before)
	// The log before the snapshot was compacted, so the state must have come
	// from a snapshot streamed over InstallSnapshot.
	if got := joiner.fsm.restoreCount(); got == 0 {
		t.Fatal("joiner caught up without installing a snapshot")
	}

	applyOne(t, leader)
	all := append(nodes, joiner)
	waitApplied(t, all, before+1)
}

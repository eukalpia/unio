package logstore

import (
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// countingFSM records the payload of every command in apply order.
type countingFSM struct {
	mu      sync.Mutex
	applied []string
}

// Apply implements raft.FSM.
func (f *countingFSM) Apply(l *raft.Log) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, string(l.Data))
	return len(f.applied)
}

// Snapshot implements raft.FSM with an empty snapshot.
func (f *countingFSM) Snapshot() (raft.FSMSnapshot, error) {
	return noopSnapshot{}, nil
}

// Restore implements raft.FSM.
func (f *countingFSM) Restore(rc io.ReadCloser) error {
	return rc.Close()
}

// entries returns a copy of the commands applied so far.
func (f *countingFSM) entries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.applied)
}

// noopSnapshot is the FSM snapshot of countingFSM. It has no state to write.
type noopSnapshot struct{}

// Persist implements raft.FSMSnapshot.
func (noopSnapshot) Persist(sink raft.SnapshotSink) error { return sink.Close() }

// Release implements raft.FSMSnapshot.
func (noopSnapshot) Release() {}

// waitForLeader blocks until r reports that it is the leader, or fails the
// test after a generous deadline. It waits on raft's leadership channel and
// never sleeps.
func waitForLeader(t *testing.T, r *raft.Raft) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case isLeader := <-r.LeaderCh():
			if isLeader {
				return
			}
		case <-deadline:
			t.Fatalf("no leader elected, state %v", r.State())
		}
	}
}

// TestSingleNodeRaftAppliesInOrder runs a one-voter hashicorp/raft cluster on
// the group's LogStore and StableStore, applies 100 commands, and checks that
// the FSM sees them exactly once and in submission order.
func TestSingleNodeRaftAppliesInOrder(t *testing.T) {
	const commands = 100
	s := newTestStore(t)
	group := s.Group(1)

	conf := raft.DefaultConfig()
	conf.LocalID = "node-1"
	conf.LogOutput = io.Discard
	conf.HeartbeatTimeout = 50 * time.Millisecond
	conf.ElectionTimeout = 50 * time.Millisecond
	conf.LeaderLeaseTimeout = 50 * time.Millisecond
	conf.CommitTimeout = 5 * time.Millisecond

	addr, trans := raft.NewInmemTransport("node-1")
	cluster := raft.Configuration{Servers: []raft.Server{
		{Suffrage: raft.Voter, ID: conf.LocalID, Address: addr},
	}}
	snaps := raft.NewInmemSnapshotStore()
	if err := raft.BootstrapCluster(conf, group, group, snaps, trans, cluster); err != nil {
		t.Fatalf("BootstrapCluster: %v", err)
	}

	fsm := &countingFSM{}
	r, err := raft.NewRaft(conf, fsm, group, group, snaps, trans)
	if err != nil {
		t.Fatalf("NewRaft: %v", err)
	}
	t.Cleanup(func() {
		if err := r.Shutdown().Error(); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	waitForLeader(t, r)

	futures := make([]raft.ApplyFuture, commands)
	want := make([]string, commands)
	for i := range futures {
		want[i] = fmt.Sprintf("cmd-%03d", i)
		futures[i] = r.Apply([]byte(want[i]), 30*time.Second)
	}
	for i, f := range futures {
		if err := f.Error(); err != nil {
			t.Fatalf("Apply %d: %v", i, err)
		}
	}

	if got := fsm.entries(); !slices.Equal(got, want) {
		t.Fatalf("FSM applied %d commands, want the %d submitted in order", len(got), commands)
	}
	last, err := group.LastIndex()
	if err != nil {
		t.Fatalf("LastIndex: %v", err)
	}
	if last < commands {
		t.Fatalf("LastIndex %d, want at least %d committed entries", last, commands)
	}
}

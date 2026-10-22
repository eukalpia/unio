package group_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/meta/raftstore/fsm"
)

// TestReadWaitsForStateMachineAfterFailover holds the state machines of both
// followers behind their logs, commits a write through the leader, and stops
// the leader. The new leader's Raft has the write committed and handed to the
// state machine, so raft's AppliedIndex covers it, but the state does not
// show it yet. A read on the new leader must wait for the state machine and
// return the write: answering from the state at that moment would lose an
// acknowledged write, which breaks linearizability.
func TestReadWaitsForStateMachineAfterFailover(t *testing.T) {
	c := newTestCluster(t, 3)
	leader := c.awaitLeader()
	if _, err := kvPut(context.Background(), leader, "seed", "s"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	gate := make(chan struct{})
	var release sync.Once
	open := func() { release.Do(func() { close(gate) }) }
	t.Cleanup(open)
	var survivors []*testNode
	for _, nd := range c.nodes {
		if nd == leader {
			continue
		}
		survivors = append(survivors, nd)
		hold := nd.fsm.Applied()
		fsm.SetApplyHookForTesting(nd.fsm, func(index uint64) {
			if index > hold {
				<-gate
			}
		})
	}

	if _, err := kvPut(context.Background(), leader, "acked", "yes"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := leader.g.Close(); err != nil {
		t.Fatalf("stop leader: %v", err)
	}
	next := c.awaitLeaderAmong(survivors)
	// Let the new leader commit its first entry. From then on its commit
	// index equals its last index, and raft's AppliedIndex has caught up,
	// so only the held state machine lags behind.
	time.Sleep(300 * time.Millisecond)

	// The state machines stay held for a while after the read starts; the
	// read must wait for them rather than answer from the stale state.
	time.AfterFunc(300*time.Millisecond, open)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	got, err := kvGet(ctx, next, "acked")
	if err != nil || got != "yes" {
		t.Fatalf("read after failover = %q, %v; want the acknowledged write", got, err)
	}
}

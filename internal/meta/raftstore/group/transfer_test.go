package group_test

import (
	"context"
	"testing"
)

// TestTransferLeadershipHandsOverToAnotherVoter checks that a leader that
// transfers leadership steps down, and that another voter takes over.
func TestTransferLeadershipHandsOverToAnotherVoter(t *testing.T) {
	c := newTestCluster(t, 3)
	old := c.awaitLeader()
	ctx, cancel := context.WithTimeout(context.Background(), electionTimeout)
	defer cancel()
	if err := old.g.TransferLeadership(ctx); err != nil {
		t.Fatalf("TransferLeadership: %v", err)
	}
	if old.g.IsLeader() {
		t.Fatal("the old leader still leads after the transfer")
	}
	next := c.awaitLeader()
	if next == old {
		t.Fatal("the transfer returned leadership to the old leader")
	}
}

// TestTransferLeadershipIsNoopWithoutOtherVoter checks that a single voter
// keeps leadership: there is nobody to hand it to.
func TestTransferLeadershipIsNoopWithoutOtherVoter(t *testing.T) {
	c := newTestCluster(t, 1)
	leader := c.awaitLeader()
	if err := leader.g.TransferLeadership(context.Background()); err != nil {
		t.Fatalf("TransferLeadership: %v", err)
	}
	if !leader.g.IsLeader() {
		t.Fatal("the only voter gave up leadership")
	}
}

// TestTransferLeadershipIsNoopOnFollower checks that a follower does nothing
// and reports no error.
func TestTransferLeadershipIsNoopOnFollower(t *testing.T) {
	c := newTestCluster(t, 3)
	leader := c.awaitLeader()
	for _, nd := range c.nodes {
		if nd == leader {
			continue
		}
		if err := nd.g.TransferLeadership(context.Background()); err != nil {
			t.Fatalf("follower %s: TransferLeadership: %v", nd.id, err)
		}
	}
	if !leader.g.IsLeader() {
		t.Fatal("a follower's transfer call removed the leader")
	}
}

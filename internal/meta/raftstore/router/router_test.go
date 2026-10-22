package router_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/metatest"
	"github.com/eukalpia/unio/internal/meta/raftstore/router"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// kvCommand returns a command that the routers below never apply: they host
// no groups, so every node answers not-leader.
func kvCommand() *pb.Command {
	return &pb.Command{Cmd: &pb.Command_KvPut{KvPut: &pb.KVPut{Key: "k", Value: []byte("v")}}}
}

// newRouters starts a router on each node. None of them hosts a group, so
// every forwarded command is answered with not-leader.
func newRouters(t *testing.T, nodes ...ids.NodeID) (*router.Router, func(ids.PartitionID) []ids.NodeID) {
	t.Helper()
	cl := metatest.NewClusterWithNodes(nodes...)
	replicas := func(ids.PartitionID) []ids.NodeID { return append([]ids.NodeID(nil), nodes...) }
	var first *router.Router
	for i, n := range nodes {
		r, err := router.New(n, cl.RPC(n), replicas)
		if err != nil {
			t.Fatalf("router %s: %v", n, err)
		}
		if i == 0 {
			first = r
		}
	}
	return first, replicas
}

// TestProposeReportsNotLeaderAfterBoundedRetries checks that when no replica
// leads, a proposal that was never appended fails with ErrNotLeader after a
// bounded number of attempts, rather than hanging.
func TestProposeReportsNotLeaderAfterBoundedRetries(t *testing.T) {
	r, _ := newRouters(t, "n1", "n2")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := r.Propose(ctx, 1, kvCommand())
	if !errors.Is(err, meta.ErrNotLeader) {
		t.Fatalf("Propose error = %v, want ErrNotLeader", err)
	}
}

// TestReadReportsNotLeaderAfterBoundedRetries checks the same for reads.
func TestReadReportsNotLeaderAfterBoundedRetries(t *testing.T) {
	r, _ := newRouters(t, "n1", "n2")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	q := &pb.ReadQuery{Query: &pb.ReadQuery_KvGet{KvGet: &pb.KVGetQuery{Key: "k"}}}
	_, err := r.Read(ctx, 1, q)
	if !errors.Is(err, meta.ErrNotLeader) {
		t.Fatalf("Read error = %v, want ErrNotLeader", err)
	}
}

// TestRetriesStopAtTheCallerDeadline checks that a retry loop honours the
// deadline of the caller instead of running its full backoff schedule.
func TestRetriesStopAtTheCallerDeadline(t *testing.T) {
	r, _ := newRouters(t, "n1", "n2")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.Propose(ctx, 1, kvCommand())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Propose error = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Propose took %v after a 50ms deadline", elapsed)
	}
}

// TestProposeFailsFastOnCancelledContext checks that a cancelled caller never
// sends a command.
func TestProposeFailsFastOnCancelledContext(t *testing.T) {
	r, _ := newRouters(t, "n1", "n2")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Propose(ctx, 1, kvCommand())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Propose error = %v, want Canceled", err)
	}
}

package metatest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// mustRegister installs h for method on rpc and fails the test on error.
func mustRegister(t *testing.T, rpc cluster.RPC, method string, h cluster.Handler) {
	t.Helper()
	if err := rpc.Register(method, h); err != nil {
		t.Fatalf("Register(%s): %v", method, err)
	}
}

// mustTransport returns node's raft transport for group.
func mustTransport(t *testing.T, c *Cluster, node ids.NodeID, group ids.PartitionID) raft.Transport {
	t.Helper()
	tr, err := c.RaftNetwork(node).Transport(group)
	if err != nil {
		t.Fatalf("Transport(%s, %d): %v", node, group, err)
	}
	return tr
}

// serveVotes answers every RequestVote arriving at tr with term. The goroutine
// stops when the test ends, and the test waits for it to exit.
func serveVotes(t *testing.T, tr raft.Transport, term uint64) {
	t.Helper()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case rpc := <-tr.Consumer():
				rpc.Respond(&raft.RequestVoteResponse{Term: term, Granted: true}, nil)
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})
}

// requestVote sends a RequestVote from one transport to a node and returns
// the term of the reply.
func requestVote(from raft.Transport, to ids.NodeID) (uint64, error) {
	var resp raft.RequestVoteResponse
	err := from.RequestVote("", raft.ServerAddress(to), &raft.RequestVoteRequest{Term: 1}, &resp)
	return resp.Term, err
}

func TestRPCRoundTripUsesWireEncoding(t *testing.T) {
	c := NewCluster(2)
	var gotFrom ids.NodeID
	mustRegister(t, c.RPC("n2"), "test.Checksum", func(_ context.Context, from ids.NodeID, req []byte) ([]byte, error) {
		var in pb.Checksum
		if err := in.UnmarshalVT(req); err != nil {
			return nil, err
		}
		gotFrom = from
		out := &pb.Checksum{
			Algorithm: in.Algorithm,
			Type:      pb.ChecksumType_CHECKSUM_TYPE_COMPOSITE,
			Value:     append([]byte(nil), in.Value...),
			Parts:     in.Parts + 1,
		}
		return out.MarshalVT()
	})

	req := &pb.Checksum{
		Algorithm: pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C,
		Value:     []byte{1, 2, 3, 4},
		Parts:     4,
	}
	var resp pb.Checksum
	if err := c.RPC("n1").Call(t.Context(), "n2", "test.Checksum", req, &resp); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotFrom != "n1" {
		t.Fatalf("handler saw caller %q, want n1", gotFrom)
	}
	if resp.Algorithm != pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C ||
		resp.Type != pb.ChecksumType_CHECKSUM_TYPE_COMPOSITE ||
		string(resp.Value) != "\x01\x02\x03\x04" || resp.Parts != 5 {
		t.Fatalf("reply = %v, want the handler's transformed checksum", &resp)
	}
}

func TestRPCErrorsKeepIdentity(t *testing.T) {
	c := NewCluster(2)
	coded := s3err.Errorf(s3err.NoSuchKey, "no such key")
	mustRegister(t, c.RPC("n2"), "test.Coded", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		return nil, coded
	})
	mustRegister(t, c.RPC("n2"), "test.Sentinel", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		return nil, meta.ErrRevisionMismatch
	})
	mustRegister(t, c.RPC("n2"), "test.Wrapped", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		return nil, fmt.Errorf("outer: %w", meta.ErrNotFound)
	})
	caller := c.RPC("n1")

	err := caller.Call(t.Context(), "n2", "test.Coded", nil, nil)
	if err != error(coded) {
		t.Fatalf("Call returned %v (%T), want the handler's own error value", err, err)
	}
	if got := s3err.CodeOf(err); got != s3err.NoSuchKey {
		t.Fatalf("CodeOf = %s, want NoSuchKey", got)
	}

	err = caller.Call(t.Context(), "n2", "test.Sentinel", nil, nil)
	if err != meta.ErrRevisionMismatch {
		t.Fatalf("Call returned %v, want the meta.ErrRevisionMismatch sentinel", err)
	}
	if !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatal("errors.Is does not match the sentinel")
	}

	err = caller.Call(t.Context(), "n2", "test.Wrapped", nil, nil)
	if !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("errors.Is does not match a wrapped sentinel: %v", err)
	}
}

// requirePlainError fails the test unless err is non-nil and carries no S3
// code, so that the router treats it as a transport failure.
func requirePlainError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("call succeeded, want an error")
	}
	var coder s3err.Coder
	if errors.As(err, &coder) {
		t.Fatalf("error %v carries S3 code %s, want a plain error", err, coder.S3Code())
	}
}

func TestCallToDownNodeFailsWithPlainError(t *testing.T) {
	c := NewCluster(2)
	mustRegister(t, c.RPC("n2"), "test.Ping", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		return nil, nil
	})
	caller := c.RPC("n1")
	mustNoErr(t, c.Kill("n2"))

	err := caller.Call(t.Context(), "n2", "test.Ping", nil, nil)
	requirePlainError(t, err)
	if got := s3err.CodeOf(err); got != s3err.InternalError {
		t.Fatalf("CodeOf = %s, want InternalError", got)
	}

	mustNoErr(t, c.Restart("n2"))
	mustNoErr(t, caller.Call(t.Context(), "n2", "test.Ping", nil, nil))
}

func TestCallFromDownNodeFails(t *testing.T) {
	c := NewCluster(2)
	mustRegister(t, c.RPC("n2"), "test.Ping", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		return nil, nil
	})
	mustNoErr(t, c.Kill("n1"))
	requirePlainError(t, c.RPC("n1").Call(t.Context(), "n2", "test.Ping", nil, nil))
}

func TestCallFailureCases(t *testing.T) {
	c := NewCluster(2)
	caller := c.RPC("n1")
	mustRegister(t, c.RPC("n2"), "test.Ping", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		return nil, nil
	})

	tests := []struct {
		name string
		call func() error
	}{
		{"no handler", func() error { return caller.Call(t.Context(), "n2", "test.Missing", nil, nil) }},
		{"unknown target", func() error { return caller.Call(t.Context(), "n9", "test.Ping", nil, nil) }},
		{"cancelled context", func() error {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return caller.Call(ctx, "n2", "test.Ping", nil, nil)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("call succeeded, want an error")
			}
		})
	}
}

func TestRegisterReplacesHandler(t *testing.T) {
	c := NewCluster(2)
	rpc := c.RPC("n2")
	caller := c.RPC("n1")
	called := ""
	mustRegister(t, rpc, "test.Record", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		called = "first"
		return nil, nil
	})
	mustRegister(t, rpc, "test.Record", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		called = "second"
		return nil, nil
	})
	mustNoErr(t, caller.Call(t.Context(), "n2", "test.Record", nil, nil))
	if called != "second" {
		t.Fatalf("handler called = %q, want the replacement", called)
	}

	// Registration survives a kill and restart, and can be repeated after one.
	mustNoErr(t, c.Kill("n2"))
	mustNoErr(t, c.Restart("n2"))
	mustRegister(t, rpc, "test.Record", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		called = "third"
		return nil, nil
	})
	mustNoErr(t, caller.Call(t.Context(), "n2", "test.Record", nil, nil))
	if called != "third" {
		t.Fatalf("handler called = %q after restart, want third", called)
	}
}

func TestRegisterFailures(t *testing.T) {
	c := NewCluster(1)
	if err := c.RPC("n1").Register("test.Nil", nil); err == nil {
		t.Fatal("Register with a nil handler succeeded")
	}
	err := c.RPC("n9").Register("test.Ping", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("Register on an unknown node succeeded")
	}
}

func TestClusterLifecycle(t *testing.T) {
	c := NewCluster(3)
	for _, n := range []ids.NodeID{"n1", "n2", "n3"} {
		if !c.IsUp(n) {
			t.Fatalf("%s is not up in a new cluster", n)
		}
	}
	mustNoErr(t, c.Kill("n2"))
	mustNoErr(t, c.Kill("n2"))
	if c.IsUp("n2") {
		t.Fatal("n2 is up after Kill")
	}
	if c.IsUp("n9") {
		t.Fatal("an unknown node reports up")
	}
	if err := c.Kill("n9"); err == nil {
		t.Fatal("Kill of an unknown node succeeded")
	}
	if err := c.Restart("n9"); err == nil {
		t.Fatal("Restart of an unknown node succeeded")
	}
	mustNoErr(t, c.Restart("n2"))
	if !c.IsUp("n2") {
		t.Fatal("n2 is not up after Restart")
	}
}

func TestNewClusterNodeIDs(t *testing.T) {
	c := NewCluster(3)
	for _, n := range []ids.NodeID{"n1", "n2", "n3"} {
		if !c.IsUp(n) {
			t.Fatalf("NewCluster(3) has no node %s", n)
		}
	}
	dup := NewClusterWithNodes("a", "b", "a")
	if !dup.IsUp("a") || !dup.IsUp("b") {
		t.Fatal("NewClusterWithNodes lost a node")
	}
	if empty := NewCluster(0); empty.IsUp("n1") {
		t.Fatal("NewCluster(0) has nodes")
	}
}

func TestRaftTransportLocalAddress(t *testing.T) {
	c := NewCluster(2)
	tr := mustTransport(t, c, "n1", 1)
	if got := string(tr.LocalAddr()); got != "n1" {
		t.Fatalf("LocalAddr = %q, want n1", got)
	}
	again := mustTransport(t, c, "n1", 1)
	if again != tr {
		t.Fatal("Transport returned a different transport for the same node and group")
	}
}

// TestRaftTransportsConnectBothWays sends votes in both directions between two
// nodes whose transports were created in different orders.
func TestRaftTransportsConnectBothWays(t *testing.T) {
	c := NewCluster(2)
	t1 := mustTransport(t, c, "n1", 1)
	t2 := mustTransport(t, c, "n2", 1)
	serveVotes(t, t1, 11)
	serveVotes(t, t2, 22)

	term, err := requestVote(t1, "n2")
	mustNoErr(t, err)
	if term != 22 {
		t.Fatalf("n1 -> n2 term = %d, want 22", term)
	}
	term, err = requestVote(t2, "n1")
	mustNoErr(t, err)
	if term != 11 {
		t.Fatalf("n2 -> n1 term = %d, want 11", term)
	}
}

// TestRaftKillSeversAndRestartReconnects checks that a killed node cannot send
// or receive raft RPCs and that Restart reconnects it with fresh transports.
func TestRaftKillSeversAndRestartReconnects(t *testing.T) {
	c := NewCluster(3)
	t1 := mustTransport(t, c, "n1", 1)
	t2 := mustTransport(t, c, "n2", 1)
	t3 := mustTransport(t, c, "n3", 1)
	serveVotes(t, t1, 1)
	serveVotes(t, t2, 2)
	serveVotes(t, t3, 3)

	term, err := requestVote(t1, "n2")
	mustNoErr(t, err)
	if term != 2 {
		t.Fatalf("before kill: term = %d, want 2", term)
	}

	mustNoErr(t, c.Kill("n2"))
	if _, err := requestVote(t1, "n2"); err == nil {
		t.Fatal("vote from n1 to killed n2 succeeded")
	}
	if _, err := requestVote(t3, "n2"); err == nil {
		t.Fatal("vote from n3 to killed n2 succeeded")
	}
	if _, err := requestVote(t2, "n1"); err == nil {
		t.Fatal("vote from killed n2 to n1 succeeded")
	}
	if term, err := requestVote(t1, "n3"); err != nil || term != 3 {
		t.Fatalf("n1 -> n3 while n2 is down: term %d, err %v", term, err)
	}

	mustNoErr(t, c.Restart("n2"))
	fresh := mustTransport(t, c, "n2", 1)
	if fresh == t2 {
		t.Fatal("Restart kept the old transport")
	}
	serveVotes(t, fresh, 2)
	term, err = requestVote(t1, "n2")
	mustNoErr(t, err)
	if term != 2 {
		t.Fatalf("after restart: n1 -> n2 term = %d, want 2", term)
	}
	term, err = requestVote(fresh, "n1")
	mustNoErr(t, err)
	if term != 1 {
		t.Fatalf("after restart: n2 -> n1 term = %d, want 1", term)
	}
}

func TestRaftGroupsAreIsolated(t *testing.T) {
	c := NewCluster(2)
	t1g1 := mustTransport(t, c, "n1", 1)
	t2g2 := mustTransport(t, c, "n2", 2)
	serveVotes(t, t2g2, 2)
	if _, err := requestVote(t1g1, "n2"); err == nil {
		t.Fatal("a group 1 transport reached a node that only has group 2")
	}
}

func TestRaftTransportOfDownNodeFails(t *testing.T) {
	c := NewCluster(1)
	mustNoErr(t, c.Kill("n1"))
	_, err := c.RaftNetwork("n1").Transport(1)
	requirePlainError(t, err)
	mustNoErr(t, c.Restart("n1"))
	mustTransport(t, c, "n1", 1)
}

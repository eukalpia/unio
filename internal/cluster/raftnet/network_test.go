package raftnet

//lint:file-ignore SA1019 Leader and Candidate are carried because hashicorp/raft 1.8 still reads them when Addr is empty; dropping them would make the wire mapping lossy.

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/cluster/clustertest"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/s3err"
)

// fakeRaft answers every RPC that arrives on a group's consumer channel. It
// records what it saw so that tests can compare it with what was sent.
type fakeRaft struct {
	t    *testing.T
	seen chan raft.RPC
}

// startFakeRaft serves consumer on a goroutine that ends with the test.
func startFakeRaft(t *testing.T, g raft.Transport) *fakeRaft {
	t.Helper()
	f := &fakeRaft{t: t, seen: make(chan raft.RPC, 16)}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	consumer := g.Consumer()
	go func() {
		for {
			select {
			case <-stop:
				return
			case rpc := <-consumer:
				f.answer(rpc)
			}
		}
	}()
	return f
}

// answer replies to one RPC with a fixed, recognizable response.
func (f *fakeRaft) answer(rpc raft.RPC) {
	f.seen <- rpc
	switch cmd := rpc.Command.(type) {
	case *raft.AppendEntriesRequest:
		rpc.Respond(&raft.AppendEntriesResponse{Term: cmd.Term, LastLog: uint64(len(cmd.Entries)), Success: true}, nil)
	case *raft.RequestVoteRequest:
		rpc.Respond(&raft.RequestVoteResponse{Term: cmd.Term, Granted: true}, nil)
	case *raft.RequestPreVoteRequest:
		rpc.Respond(&raft.RequestPreVoteResponse{Term: cmd.Term, Granted: false}, nil)
	case *raft.TimeoutNowRequest:
		rpc.Respond(&raft.TimeoutNowResponse{}, nil)
	case *raft.InstallSnapshotRequest:
		data, err := io.ReadAll(rpc.Reader)
		if err != nil {
			rpc.Respond(nil, err)
			return
		}
		rpc.Respond(&raft.InstallSnapshotResponse{Term: cmd.Term, Success: int64(len(data)) == cmd.Size}, nil)
	default:
		rpc.Respond(nil, errors.New("unexpected command"))
	}
}

// netPair is two nodes "a" and "b", each with a raftnet Network.
type netPair struct {
	cluster *clustertest.Cluster
	a, b    *Network
}

// newNetPair builds the pair on one loopback cluster.
func newNetPair(t *testing.T) *netPair {
	t.Helper()
	c := clustertest.NewCluster(t, "a", "b")
	a, err := New(c.Nodes["a"], "a", Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(c.Nodes["b"], "b", Options{})
	if err != nil {
		t.Fatal(err)
	}
	return &netPair{cluster: c, a: a, b: b}
}

// peerTransport returns the group-0 transport of n, failing the test on error.
func peerTransport(t *testing.T, n *Network) raft.Transport {
	t.Helper()
	tr, err := n.Transport(ids.SystemPartition)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestTransportIsCachedPerGroup(t *testing.T) {
	p := newNetPair(t)
	first := peerTransport(t, p.a)
	again := peerTransport(t, p.a)
	if first != again {
		t.Fatal("same group returned two transports")
	}
	other, err := p.a.Transport(7)
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatal("different groups share a transport")
	}
}

func TestNewRejectsSecondNetworkOnOneTransport(t *testing.T) {
	c := clustertest.NewCluster(t, "a")
	if _, err := New(c.Nodes["a"], "a", Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(c.Nodes["a"], "a", Options{}); err == nil {
		t.Fatal("two networks registered the same methods on one transport")
	}
}

func TestAppendEntriesCrossesTheWire(t *testing.T) {
	p := newNetPair(t)
	server := peerTransport(t, p.a)
	client := peerTransport(t, p.b)
	fake := startFakeRaft(t, server)

	req := &raft.AppendEntriesRequest{
		RPCHeader:    raft.RPCHeader{ProtocolVersion: raft.ProtocolVersionMax, ID: []byte("b"), Addr: []byte("b")},
		Term:         4,
		Leader:       []byte("b"),
		PrevLogEntry: 9,
		PrevLogTerm:  3,
		Entries:      []*raft.Log{{Index: 10, Term: 4, Data: []byte("payload")}},
	}
	var resp raft.AppendEntriesResponse
	if err := client.AppendEntries("b", "a", req, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.Term != 4 || resp.LastLog != 1 {
		t.Fatalf("response = %+v", resp)
	}
	got := (<-fake.seen).Command.(*raft.AppendEntriesRequest)
	if d := cmp.Diff(req, got, cmpopts.EquateEmpty()); d != "" {
		t.Fatalf("server saw a different request:\n%s", d)
	}
}

func TestHeartbeatUsesCallbackNotConsumer(t *testing.T) {
	p := newNetPair(t)
	server := peerTransport(t, p.a)
	client := peerTransport(t, p.b)
	consumer := server.Consumer()
	heard := make(chan raft.RPC, 1)
	server.SetHeartbeatHandler(func(rpc raft.RPC) {
		heard <- rpc
		rpc.Respond(&raft.AppendEntriesResponse{Term: 2, Success: true}, nil)
	})

	req := &raft.AppendEntriesRequest{
		RPCHeader: raft.RPCHeader{ProtocolVersion: raft.ProtocolVersionMax, ID: []byte("b")},
		Term:      2,
		Leader:    []byte("b"),
	}
	var resp raft.AppendEntriesResponse
	if err := client.AppendEntries("b", "a", req, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("heartbeat response = %+v", resp)
	}
	select {
	case <-heard:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat callback not invoked")
	}
	select {
	case rpc := <-consumer:
		t.Fatalf("heartbeat leaked to the consumer: %T", rpc.Command)
	default:
	}
}

func TestVoteAndTransferRPCs(t *testing.T) {
	p := newNetPair(t)
	server := peerTransport(t, p.a)
	client := peerTransport(t, p.b)
	startFakeRaft(t, server)

	var vote raft.RequestVoteResponse
	if err := client.RequestVote("b", "a", &raft.RequestVoteRequest{
		RPCHeader: raft.RPCHeader{ID: []byte("b")}, Term: 6, Candidate: []byte("b"),
	}, &vote); err != nil {
		t.Fatal(err)
	}
	if !vote.Granted || vote.Term != 6 {
		t.Fatalf("vote = %+v", vote)
	}

	var pre raft.RequestPreVoteResponse
	pv, ok := client.(raft.WithPreVote)
	if !ok {
		t.Fatal("group transport does not implement pre-vote")
	}
	if err := pv.RequestPreVote("b", "a", &raft.RequestPreVoteRequest{RPCHeader: raft.RPCHeader{ID: []byte("b")}, Term: 6}, &pre); err != nil {
		t.Fatal(err)
	}
	if pre.Granted || pre.Term != 6 {
		t.Fatalf("pre-vote = %+v", pre)
	}

	if err := client.TimeoutNow("b", "a", &raft.TimeoutNowRequest{RPCHeader: raft.RPCHeader{ID: []byte("b")}}, &raft.TimeoutNowResponse{}); err != nil {
		t.Fatalf("timeout-now: %v", err)
	}
}

func TestInstallSnapshotStreamsTheBody(t *testing.T) {
	p := newNetPair(t)
	server := peerTransport(t, p.a)
	client := peerTransport(t, p.b)
	startFakeRaft(t, server)

	data := make([]byte, 3<<20)
	_, _ = rand.Read(data)
	req := &raft.InstallSnapshotRequest{
		RPCHeader: raft.RPCHeader{ID: []byte("b")},
		Term:      3,
		Leader:    []byte("b"),
		Size:      int64(len(data)),
	}
	var resp raft.InstallSnapshotResponse
	if err := client.InstallSnapshot("b", "a", req, &resp, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatal("snapshot not applied: body length or content mismatch")
	}
}

func TestInstallSnapshotRejectsShortSource(t *testing.T) {
	p := newNetPair(t)
	server := peerTransport(t, p.a)
	client := peerTransport(t, p.b)
	startFakeRaft(t, server)
	req := &raft.InstallSnapshotRequest{RPCHeader: raft.RPCHeader{ID: []byte("b")}, Size: 100}
	var resp raft.InstallSnapshotResponse
	if err := client.InstallSnapshot("b", "a", req, &resp, bytes.NewReader(make([]byte, 10))); err == nil {
		t.Fatal("snapshot with a short source accepted")
	}
}

func TestSpoofedSenderIsRejected(t *testing.T) {
	p := newNetPair(t)
	server := peerTransport(t, p.a)
	client := peerTransport(t, p.b)
	startFakeRaft(t, server)

	// Node b claims to be node "x" in the header. Raft would believe it.
	req := &raft.AppendEntriesRequest{RPCHeader: raft.RPCHeader{ID: []byte("x")}, Term: 1, Leader: []byte("x")}
	var resp raft.AppendEntriesResponse
	err := client.AppendEntries("b", "a", req, &resp)
	if s3err.CodeOf(err) != s3err.AccessDenied {
		t.Fatalf("spoofed append accepted: %v", err)
	}
}

func TestUnknownGroupIsRejected(t *testing.T) {
	p := newNetPair(t)
	// Group 9 was never created on a, so a cannot serve it yet.
	other, err := p.b.Transport(9)
	if err != nil {
		t.Fatal(err)
	}
	req := &raft.AppendEntriesRequest{RPCHeader: raft.RPCHeader{ID: []byte("b")}, Term: 1, Leader: []byte("b")}
	var resp raft.AppendEntriesResponse
	if err := other.AppendEntries("b", "a", req, &resp); err == nil {
		t.Fatal("request for a group not hosted on the target accepted")
	}
}

func TestUnreachableTargetIsAnError(t *testing.T) {
	p := newNetPair(t)
	client := peerTransport(t, p.b)
	req := &raft.RequestVoteRequest{RPCHeader: raft.RPCHeader{ID: []byte("b")}, Term: 1}
	var resp raft.RequestVoteResponse
	if err := client.RequestVote("b", "ghost", req, &resp); err == nil {
		t.Fatal("vote to an unknown node succeeded")
	}
}

func TestPipelineIsUnsupported(t *testing.T) {
	p := newNetPair(t)
	tr := peerTransport(t, p.b)
	if _, err := tr.AppendEntriesPipeline("a", "a"); !errors.Is(err, raft.ErrPipelineReplicationNotSupported) {
		t.Fatalf("pipeline error = %v", err)
	}
}

func TestAddressEncodingRoundTrips(t *testing.T) {
	p := newNetPair(t)
	tr := peerTransport(t, p.a)
	enc := tr.EncodePeer("node-7", "node-7")
	if got := tr.DecodePeer(enc); got != "node-7" {
		t.Fatalf("decoded %q", got)
	}
	if tr.LocalAddr() != "a" {
		t.Fatalf("local address = %q", tr.LocalAddr())
	}
}

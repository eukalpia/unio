package raftnet

//lint:file-ignore SA1019 the tests send and inspect the legacy Leader and Candidate fields that raft 1.8 still reads.

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/cluster/uniwire"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// claim is the identity a request carries: the header ID and address, and the
// legacy Leader or Candidate field, which raft reads when the address is empty.
type claim struct {
	id, addr, legacy string
}

// identityCases are the claims node "b" can make. The honest claims name b in
// every field. Each spoofed claim names node c in at least one field.
var identityCases = []struct {
	name   string
	claim  claim
	accept bool
}{
	{name: "honest", claim: claim{id: "b", addr: "b", legacy: "b"}, accept: true},
	{name: "honest without address", claim: claim{id: "b", addr: "", legacy: "b"}, accept: true},
	{name: "address names another node", claim: claim{id: "b", addr: "c", legacy: "b"}, accept: false},
	{name: "legacy field names another node", claim: claim{id: "b", addr: "", legacy: "c"}, accept: false},
	{name: "legacy field names another node beside an honest address", claim: claim{id: "b", addr: "b", legacy: "c"}, accept: false},
	{name: "header id names another node", claim: claim{id: "c", addr: "c", legacy: "c"}, accept: false},
}

// identityOf returns the identity fields of a delivered raft command.
type identityOf func(cmd any) (id, addr, legacy []byte, hasLegacy bool)

// rpcUnderTest is one Raft RPC type, as sent from node b to node a.
type rpcUnderTest struct {
	name string
	// legacy reports whether the RPC carries a legacy identity field.
	legacy bool
	send   func(ctx context.Context, tr *uniwire.Transport, c claim) error
	fields identityOf
}

// rpcHeader is the header a request from claim c carries.
func rpcHeader(c claim) *uniov1.RaftHeader {
	return &uniov1.RaftHeader{Id: []byte(c.id), Addr: []byte(c.addr)}
}

// rpcsUnderTest covers every Raft request type that names a sender.
var rpcsUnderTest = []rpcUnderTest{
	{
		name:   "AppendEntries",
		legacy: true,
		send: func(ctx context.Context, tr *uniwire.Transport, c claim) error {
			var out uniov1.RaftAppendEntriesResponse
			return tr.Call(ctx, "a", MethodAppendEntries, &uniov1.RaftAppendEntries{
				Header: rpcHeader(c),
				Leader: []byte(c.legacy),
				Term:   1,
			}, &out)
		},
		fields: func(cmd any) ([]byte, []byte, []byte, bool) {
			r := cmd.(*raft.AppendEntriesRequest)
			return r.ID, r.Addr, r.Leader, true
		},
	},
	{
		name:   "RequestVote",
		legacy: true,
		send: func(ctx context.Context, tr *uniwire.Transport, c claim) error {
			var out uniov1.RaftRequestVoteResponse
			return tr.Call(ctx, "a", MethodRequestVote, &uniov1.RaftRequestVote{
				Header:    rpcHeader(c),
				Candidate: []byte(c.legacy),
				Term:      2,
			}, &out)
		},
		fields: func(cmd any) ([]byte, []byte, []byte, bool) {
			r := cmd.(*raft.RequestVoteRequest)
			return r.ID, r.Addr, r.Candidate, true
		},
	},
	{
		name:   "RequestPreVote",
		legacy: false,
		send: func(ctx context.Context, tr *uniwire.Transport, c claim) error {
			var out uniov1.RaftRequestPreVoteResponse
			return tr.Call(ctx, "a", MethodRequestPreVote, &uniov1.RaftRequestPreVote{
				Header: rpcHeader(c),
				Term:   2,
			}, &out)
		},
		fields: func(cmd any) ([]byte, []byte, []byte, bool) {
			r := cmd.(*raft.RequestPreVoteRequest)
			return r.ID, r.Addr, nil, false
		},
	},
	{
		name:   "TimeoutNow",
		legacy: false,
		send: func(ctx context.Context, tr *uniwire.Transport, c claim) error {
			var out uniov1.RaftTimeoutNowResponse
			return tr.Call(ctx, "a", MethodTimeoutNow, &uniov1.RaftTimeoutNow{
				Header: rpcHeader(c),
			}, &out)
		},
		fields: func(cmd any) ([]byte, []byte, []byte, bool) {
			r := cmd.(*raft.TimeoutNowRequest)
			return r.ID, r.Addr, nil, false
		},
	},
	{
		name:   "InstallSnapshot",
		legacy: true,
		send: func(ctx context.Context, tr *uniwire.Transport, c claim) error {
			payload, err := (&uniov1.RaftInstallSnapshot{
				Header: rpcHeader(c),
				Leader: []byte(c.legacy),
			}).MarshalVT()
			if err != nil {
				return err
			}
			rep, err := tr.Stream(ctx, "a", MethodInstallSnapshot, payload, nil, 0)
			if err != nil {
				return err
			}
			if rep.Body != nil {
				_ = rep.Body.Close()
			}
			return nil
		},
		fields: func(cmd any) ([]byte, []byte, []byte, bool) {
			r := cmd.(*raft.InstallSnapshotRequest)
			return r.ID, r.Addr, r.Leader, true
		},
	},
}

// TestRaftRequestsAreBoundToTheirSender sends every request type from node b
// with each identity claim. An honest request is delivered to raft with every
// identity field naming b. A spoofed request is refused with AccessDenied and
// never reaches raft.
func TestRaftRequestsAreBoundToTheirSender(t *testing.T) {
	for _, rpc := range rpcsUnderTest {
		t.Run(rpc.name, func(t *testing.T) {
			p := newNetPair(t)
			fake := startFakeRaft(t, peerTransport(t, p.a))
			sender := p.cluster.Nodes["b"]
			for _, tc := range identityCases {
				if !rpc.legacy && tc.claim.legacy != tc.claim.id {
					// The RPC has no legacy field, so a legacy claim says nothing.
					continue
				}
				t.Run(tc.name, func(t *testing.T) {
					// Each case judges only its own request: discard commands that
					// earlier cases delivered.
					for len(fake.seen) > 0 {
						<-fake.seen
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					err := rpc.send(ctx, sender, tc.claim)
					if !tc.accept {
						if s3err.CodeOf(err) != s3err.AccessDenied {
							t.Fatalf("spoofed request not refused with AccessDenied: %v", err)
						}
						if n := len(fake.seen); n != 0 {
							t.Fatalf("spoofed request delivered to raft (%d commands)", n)
						}
						return
					}
					if err != nil {
						t.Fatalf("honest request refused: %v", err)
					}
					if n := len(fake.seen); n != 1 {
						t.Fatalf("honest request delivered %d times, want 1", n)
					}
					cmd := (<-fake.seen).Command
					id, addr, legacy, hasLegacy := rpc.fields(cmd)
					if string(id) != "b" {
						t.Fatalf("raft saw id %q, want b", id)
					}
					if string(addr) != "b" {
						t.Fatalf("raft saw address %q, want b", addr)
					}
					if hasLegacy && string(legacy) != "b" {
						t.Fatalf("raft saw legacy identity %q, want b", legacy)
					}
				})
			}
		})
	}
}

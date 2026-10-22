package raftnet

//lint:file-ignore SA1019 Leader and Candidate are carried because hashicorp/raft 1.8 still reads them when Addr is empty; dropping them would make the wire mapping lossy.

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// header returns a raft header naming node id.
func header(id string) raft.RPCHeader {
	return raft.RPCHeader{ProtocolVersion: raft.ProtocolVersionMax, ID: []byte(id), Addr: []byte(id)}
}

// roundTrip marshals a protobuf message and decodes it into out.
func roundTrip(t *testing.T, in interface {
	MarshalVT() ([]byte, error)
}, out interface {
	UnmarshalVT([]byte) error
},
) {
	t.Helper()
	b, err := in.MarshalVT()
	if err != nil {
		t.Fatal(err)
	}
	if err := out.UnmarshalVT(b); err != nil {
		t.Fatal(err)
	}
}

func TestAppendEntriesRoundTrip(t *testing.T) {
	now := time.Unix(1700000000, 123).UTC()
	cases := []struct {
		name string
		req  *raft.AppendEntriesRequest
	}{
		{
			name: "entries",
			req: &raft.AppendEntriesRequest{
				RPCHeader:         header("leader"),
				Term:              7,
				Leader:            []byte("leader"),
				PrevLogEntry:      41,
				PrevLogTerm:       6,
				LeaderCommitIndex: 40,
				Entries: []*raft.Log{
					{Index: 42, Term: 7, Type: raft.LogCommand, Data: []byte("a"), Extensions: []byte("x"), AppendedAt: now},
					{Index: 43, Term: 7, Type: raft.LogNoop},
				},
			},
		},
		{
			name: "heartbeat",
			req: &raft.AppendEntriesRequest{
				RPCHeader: header("leader"),
				Term:      3,
				Leader:    []byte("leader"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p uniov1.RaftAppendEntries
			roundTrip(t, appendEntriesToPB(4, tc.req), &p)
			if p.GetGroup() != 4 {
				t.Fatalf("group = %d", p.GetGroup())
			}
			got := appendEntriesFromPB(&p)
			if d := cmp.Diff(tc.req, got, cmpopts.EquateEmpty()); d != "" {
				t.Fatalf("round trip differs:\n%s", d)
			}
		})
	}
}

func TestAppendEntriesResponseRoundTrip(t *testing.T) {
	in := &raft.AppendEntriesResponse{RPCHeader: header("follower"), Term: 9, LastLog: 88, Success: true, NoRetryBackoff: true}
	var p uniov1.RaftAppendEntriesResponse
	roundTrip(t, appendEntriesRespToPB(in), &p)
	if d := cmp.Diff(in, appendEntriesRespFromPB(&p)); d != "" {
		t.Fatalf("round trip differs:\n%s", d)
	}
}

func TestVoteMessagesRoundTrip(t *testing.T) {
	rv := &raft.RequestVoteRequest{RPCHeader: header("c1"), Term: 5, Candidate: []byte("c1"), LastLogIndex: 10, LastLogTerm: 4, LeadershipTransfer: true}
	var pv uniov1.RaftRequestVote
	roundTrip(t, requestVoteToPB(2, rv), &pv)
	if d := cmp.Diff(rv, requestVoteFromPB(&pv)); d != "" {
		t.Fatalf("request vote differs:\n%s", d)
	}

	rvr := &raft.RequestVoteResponse{RPCHeader: header("v1"), Term: 5, Peers: []byte("p"), Granted: true}
	var pvr uniov1.RaftRequestVoteResponse
	roundTrip(t, requestVoteRespToPB(rvr), &pvr)
	if d := cmp.Diff(rvr, requestVoteRespFromPB(&pvr)); d != "" {
		t.Fatalf("vote response differs:\n%s", d)
	}

	pre := &raft.RequestPreVoteRequest{RPCHeader: header("c1"), Term: 6, LastLogIndex: 1, LastLogTerm: 2}
	var pp uniov1.RaftRequestPreVote
	roundTrip(t, preVoteToPB(0, pre), &pp)
	if d := cmp.Diff(pre, preVoteFromPB(&pp)); d != "" {
		t.Fatalf("pre-vote differs:\n%s", d)
	}

	prer := &raft.RequestPreVoteResponse{RPCHeader: header("v2"), Term: 6, Granted: true}
	var ppr uniov1.RaftRequestPreVoteResponse
	roundTrip(t, preVoteRespToPB(prer), &ppr)
	if d := cmp.Diff(prer, preVoteRespFromPB(&ppr)); d != "" {
		t.Fatalf("pre-vote response differs:\n%s", d)
	}
}

func TestInstallSnapshotRoundTrip(t *testing.T) {
	in := &raft.InstallSnapshotRequest{
		RPCHeader:          header("leader"),
		SnapshotVersion:    raft.SnapshotVersionMax,
		Term:               8,
		Leader:             []byte("leader"),
		LastLogIndex:       100,
		LastLogTerm:        7,
		Peers:              []byte("peers"),
		Configuration:      []byte("config"),
		ConfigurationIndex: 12,
		Size:               1 << 30,
	}
	var p uniov1.RaftInstallSnapshot
	roundTrip(t, installSnapshotToPB(1, in), &p)
	if d := cmp.Diff(in, installSnapshotFromPB(&p), cmpopts.EquateEmpty()); d != "" {
		t.Fatalf("snapshot request differs:\n%s", d)
	}

	resp := &raft.InstallSnapshotResponse{RPCHeader: header("f"), Term: 8, Success: true}
	var pr uniov1.RaftInstallSnapshotResponse
	roundTrip(t, installSnapshotRespToPB(resp), &pr)
	if d := cmp.Diff(resp, installSnapshotRespFromPB(&pr)); d != "" {
		t.Fatalf("snapshot response differs:\n%s", d)
	}
}

func TestTimeoutNowRoundTrip(t *testing.T) {
	req := &raft.TimeoutNowRequest{RPCHeader: header("old-leader")}
	var p uniov1.RaftTimeoutNow
	roundTrip(t, timeoutNowToPB(3, req), &p)
	if d := cmp.Diff(req, timeoutNowFromPB(&p)); d != "" {
		t.Fatalf("timeout-now differs:\n%s", d)
	}
	resp := &raft.TimeoutNowResponse{RPCHeader: header("new")}
	var pr uniov1.RaftTimeoutNowResponse
	roundTrip(t, timeoutNowRespToPB(resp), &pr)
	if d := cmp.Diff(resp, timeoutNowRespFromPB(&pr)); d != "" {
		t.Fatalf("timeout-now response differs:\n%s", d)
	}
}

func TestIsHeartbeatMatchesRaftPredicate(t *testing.T) {
	cases := []struct {
		name string
		req  raft.AppendEntriesRequest
		want bool
	}{
		{name: "bare heartbeat", req: raft.AppendEntriesRequest{Term: 1, Leader: []byte("l")}, want: true},
		{name: "addr instead of leader", req: raft.AppendEntriesRequest{Term: 1, RPCHeader: raft.RPCHeader{Addr: []byte("l")}}, want: true},
		{name: "no term", req: raft.AppendEntriesRequest{Leader: []byte("l")}, want: false},
		{name: "no leader", req: raft.AppendEntriesRequest{Term: 1}, want: false},
		{name: "carries entries", req: raft.AppendEntriesRequest{Term: 1, Leader: []byte("l"), Entries: []*raft.Log{{}}}, want: false},
		{name: "prev index set", req: raft.AppendEntriesRequest{Term: 1, Leader: []byte("l"), PrevLogEntry: 1}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHeartbeat(&tc.req); got != tc.want {
				t.Fatalf("isHeartbeat = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckSenderRejectsForeignIdentity(t *testing.T) {
	h := headerToPB(header("node-b"))
	if err := checkSender(ids.NodeID("node-b"), h); err != nil {
		t.Fatalf("matching sender rejected: %v", err)
	}
	if err := checkSender(ids.NodeID("node-c"), h); err == nil {
		t.Fatal("header naming another node accepted")
	}
}

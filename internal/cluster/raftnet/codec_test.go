package raftnet

//lint:file-ignore SA1019 the tests build raft requests with Leader and Candidate, which raft 1.8 still reads.

import (
	"math"
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

// roundTrip marshals msg, which an encoder produced with err, and decodes it
// into out. Any encoder error fails the test.
func roundTrip(t *testing.T, msg interface{ MarshalVT() ([]byte, error) }, err error, out interface{ UnmarshalVT([]byte) error }) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	b, err := msg.MarshalVT()
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
			pb, err := appendEntriesToPB(4, tc.req)
			var p uniov1.RaftAppendEntries
			roundTrip(t, pb, err, &p)
			if p.GetGroup() != 4 {
				t.Fatalf("group = %d", p.GetGroup())
			}
			got, err := appendEntriesFromPB(&p)
			if err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(tc.req, got, cmpopts.EquateEmpty()); d != "" {
				t.Fatalf("round trip differs:\n%s", d)
			}
		})
	}
}

func TestAppendEntriesResponseRoundTrip(t *testing.T) {
	in := &raft.AppendEntriesResponse{RPCHeader: header("follower"), Term: 9, LastLog: 88, Success: true, NoRetryBackoff: true}
	pb, err := appendEntriesRespToPB(in)
	var p uniov1.RaftAppendEntriesResponse
	roundTrip(t, pb, err, &p)
	got, err := appendEntriesRespFromPB(&p)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(in, got); d != "" {
		t.Fatalf("round trip differs:\n%s", d)
	}
}

func TestVoteMessagesRoundTrip(t *testing.T) {
	rv := &raft.RequestVoteRequest{RPCHeader: header("c1"), Term: 5, Candidate: []byte("c1"), LastLogIndex: 10, LastLogTerm: 4, LeadershipTransfer: true}
	pv, err := requestVoteToPB(2, rv)
	var pvm uniov1.RaftRequestVote
	roundTrip(t, pv, err, &pvm)
	gotRV, err := requestVoteFromPB(&pvm)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(rv, gotRV); d != "" {
		t.Fatalf("request vote differs:\n%s", d)
	}

	rvr := &raft.RequestVoteResponse{RPCHeader: header("v1"), Term: 5, Peers: []byte("p"), Granted: true}
	pvr, err := requestVoteRespToPB(rvr)
	var pvrm uniov1.RaftRequestVoteResponse
	roundTrip(t, pvr, err, &pvrm)
	gotRVR, err := requestVoteRespFromPB(&pvrm)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(rvr, gotRVR); d != "" {
		t.Fatalf("vote response differs:\n%s", d)
	}

	pre := &raft.RequestPreVoteRequest{RPCHeader: header("c1"), Term: 6, LastLogIndex: 1, LastLogTerm: 2}
	pp, err := preVoteToPB(0, pre)
	var ppm uniov1.RaftRequestPreVote
	roundTrip(t, pp, err, &ppm)
	gotPre, err := preVoteFromPB(&ppm)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(pre, gotPre); d != "" {
		t.Fatalf("pre-vote differs:\n%s", d)
	}

	prer := &raft.RequestPreVoteResponse{RPCHeader: header("v2"), Term: 6, Granted: true}
	ppr, err := preVoteRespToPB(prer)
	var pprm uniov1.RaftRequestPreVoteResponse
	roundTrip(t, ppr, err, &pprm)
	gotPreR, err := preVoteRespFromPB(&pprm)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(prer, gotPreR); d != "" {
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
	pb, err := installSnapshotToPB(1, in)
	var p uniov1.RaftInstallSnapshot
	roundTrip(t, pb, err, &p)
	got, err := installSnapshotFromPB(&p)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(in, got, cmpopts.EquateEmpty()); d != "" {
		t.Fatalf("snapshot request differs:\n%s", d)
	}

	resp := &raft.InstallSnapshotResponse{RPCHeader: header("f"), Term: 8, Success: true}
	pr, err := installSnapshotRespToPB(resp)
	var prm uniov1.RaftInstallSnapshotResponse
	roundTrip(t, pr, err, &prm)
	gotResp, err := installSnapshotRespFromPB(&prm)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(resp, gotResp); d != "" {
		t.Fatalf("snapshot response differs:\n%s", d)
	}
}

func TestTimeoutNowRoundTrip(t *testing.T) {
	req := &raft.TimeoutNowRequest{RPCHeader: header("old-leader")}
	pb, err := timeoutNowToPB(3, req)
	var p uniov1.RaftTimeoutNow
	roundTrip(t, pb, err, &p)
	got, err := timeoutNowFromPB(&p)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(req, got); d != "" {
		t.Fatalf("timeout-now differs:\n%s", d)
	}
	resp := &raft.TimeoutNowResponse{RPCHeader: header("new")}
	pr, err := timeoutNowRespToPB(resp)
	var prm uniov1.RaftTimeoutNowResponse
	roundTrip(t, pr, err, &prm)
	gotResp, err := timeoutNowRespFromPB(&prm)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(resp, gotResp); d != "" {
		t.Fatalf("timeout-now response differs:\n%s", d)
	}
}

// TestProtocolVersionBoundaries checks both directions of the protocol version
// conversion at the edges of what raft supports.
func TestProtocolVersionBoundaries(t *testing.T) {
	encode := []struct {
		name string
		v    raft.ProtocolVersion
		ok   bool
	}{
		{name: "minimum", v: raft.ProtocolVersionMin, ok: true},
		{name: "maximum", v: raft.ProtocolVersionMax, ok: true},
		{name: "below minimum", v: raft.ProtocolVersionMin - 1, ok: false},
		{name: "above maximum", v: raft.ProtocolVersionMax + 1, ok: false},
		{name: "int max", v: math.MaxInt, ok: false},
	}
	for _, tc := range encode {
		t.Run("encode "+tc.name, func(t *testing.T) {
			got, err := protocolVersionToWire(tc.v)
			if (err == nil) != tc.ok {
				t.Fatalf("encode %d: err = %v, want ok=%v", tc.v, err, tc.ok)
			}
			if err == nil && got != uint32(tc.v) {
				t.Fatalf("encode %d = %d", tc.v, got)
			}
		})
	}
	decode := []struct {
		name string
		v    uint32
		ok   bool
	}{
		{name: "zero", v: 0, ok: true},
		{name: "maximum", v: raft.ProtocolVersionMax, ok: true},
		{name: "above maximum", v: raft.ProtocolVersionMax + 1, ok: false},
		{name: "uint32 max", v: math.MaxUint32, ok: false},
	}
	for _, tc := range decode {
		t.Run("decode "+tc.name, func(t *testing.T) {
			got, err := protocolVersionFromWire(tc.v)
			if (err == nil) != tc.ok {
				t.Fatalf("decode %d: err = %v, want ok=%v", tc.v, err, tc.ok)
			}
			if err == nil && uint32(got) != tc.v {
				t.Fatalf("decode %d = %d", tc.v, got)
			}
		})
	}
}

// TestSnapshotVersionBoundaries checks the snapshot version conversion.
func TestSnapshotVersionBoundaries(t *testing.T) {
	if _, err := snapshotVersionToWire(raft.SnapshotVersionMin); err != nil {
		t.Fatalf("minimum rejected: %v", err)
	}
	if _, err := snapshotVersionToWire(raft.SnapshotVersionMax); err != nil {
		t.Fatalf("maximum rejected: %v", err)
	}
	if _, err := snapshotVersionToWire(raft.SnapshotVersionMin - 1); err == nil {
		t.Fatal("negative snapshot version encoded")
	}
	if _, err := snapshotVersionToWire(raft.SnapshotVersionMax + 1); err == nil {
		t.Fatal("unsupported snapshot version encoded")
	}
	if _, err := snapshotVersionFromWire(raft.SnapshotVersionMax); err != nil {
		t.Fatalf("maximum decode rejected: %v", err)
	}
	if _, err := snapshotVersionFromWire(raft.SnapshotVersionMax + 1); err == nil {
		t.Fatal("unsupported snapshot version decoded")
	}
	if _, err := snapshotVersionFromWire(math.MaxUint32); err == nil {
		t.Fatal("uint32 max snapshot version decoded")
	}
}

// TestLogTypeBoundaries checks that a log type decodes only within one byte.
func TestLogTypeBoundaries(t *testing.T) {
	cases := []struct {
		name string
		v    uint32
		ok   bool
	}{
		{name: "zero", v: 0, ok: true},
		{name: "one byte maximum", v: math.MaxUint8, ok: true},
		{name: "two bytes", v: math.MaxUint8 + 1, ok: false},
		{name: "uint32 maximum", v: math.MaxUint32, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := logTypeFromWire(tc.v); (err == nil) != tc.ok {
				t.Fatalf("log type %d: err = %v, want ok=%v", tc.v, err, tc.ok)
			}
			// The same value inside an entry must fail the whole request.
			entry := &uniov1.RaftAppendEntries{
				Header:  &uniov1.RaftHeader{},
				Entries: []*uniov1.RaftLog{{Type: tc.v}},
			}
			_, err := appendEntriesFromPB(entry)
			if (err == nil) != tc.ok {
				t.Fatalf("entry with type %d: err = %v, want ok=%v", tc.v, err, tc.ok)
			}
		})
	}
}

// TestEncodersRefuseUnsupportedValues checks that a request carrying a value
// its wire field cannot represent fails to encode, rather than being sent
// truncated.
func TestEncodersRefuseUnsupportedValues(t *testing.T) {
	bad := raft.RPCHeader{ProtocolVersion: raft.ProtocolVersionMax + 1, ID: []byte("b")}
	if _, err := appendEntriesToPB(1, &raft.AppendEntriesRequest{RPCHeader: bad}); err == nil {
		t.Fatal("append entries with an unsupported protocol version encoded")
	}
	if _, err := requestVoteToPB(1, &raft.RequestVoteRequest{RPCHeader: bad}); err == nil {
		t.Fatal("vote with an unsupported protocol version encoded")
	}
	if _, err := preVoteToPB(1, &raft.RequestPreVoteRequest{RPCHeader: bad}); err == nil {
		t.Fatal("pre-vote with an unsupported protocol version encoded")
	}
	if _, err := timeoutNowToPB(1, &raft.TimeoutNowRequest{RPCHeader: bad}); err == nil {
		t.Fatal("timeout-now with an unsupported protocol version encoded")
	}
	if _, err := appendEntriesRespToPB(&raft.AppendEntriesResponse{RPCHeader: bad}); err == nil {
		t.Fatal("append response with an unsupported protocol version encoded")
	}
	snap := &raft.InstallSnapshotRequest{RPCHeader: raft.RPCHeader{ID: []byte("b")}, SnapshotVersion: raft.SnapshotVersionMax + 1}
	if _, err := installSnapshotToPB(1, snap); err == nil {
		t.Fatal("snapshot with an unsupported snapshot version encoded")
	}
}

// TestDecodersRefuseUnsupportedHeaders checks that a header with an unsupported
// protocol version makes every request decoder fail.
func TestDecodersRefuseUnsupportedHeaders(t *testing.T) {
	badHeader := &uniov1.RaftHeader{ProtocolVersion: math.MaxUint32, Id: []byte("b")}
	if _, err := appendEntriesFromPB(&uniov1.RaftAppendEntries{Header: badHeader}); err == nil {
		t.Fatal("append entries with a wrapped protocol version decoded")
	}
	if _, err := requestVoteFromPB(&uniov1.RaftRequestVote{Header: badHeader}); err == nil {
		t.Fatal("vote with a wrapped protocol version decoded")
	}
	if _, err := preVoteFromPB(&uniov1.RaftRequestPreVote{Header: badHeader}); err == nil {
		t.Fatal("pre-vote with a wrapped protocol version decoded")
	}
	if _, err := timeoutNowFromPB(&uniov1.RaftTimeoutNow{Header: badHeader}); err == nil {
		t.Fatal("timeout-now with a wrapped protocol version decoded")
	}
	if _, err := installSnapshotFromPB(&uniov1.RaftInstallSnapshot{Header: &uniov1.RaftHeader{}, SnapshotVersion: 2}); err == nil {
		t.Fatal("snapshot with an unsupported snapshot version decoded")
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
	h := &uniov1.RaftHeader{Id: []byte("node-b")}
	if err := checkSender(ids.NodeID("node-b"), h); err != nil {
		t.Fatalf("matching sender rejected: %v", err)
	}
	if err := checkSender(ids.NodeID("node-c"), h); err == nil {
		t.Fatal("header naming another node accepted")
	}
}

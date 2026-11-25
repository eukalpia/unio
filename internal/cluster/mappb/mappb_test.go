package mappb

import (
	"math"
	"math/rand"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// drive returns a deterministic drive id.
func drive(n byte) ids.DriveID {
	var id ids.DriveID
	for i := range id {
		id[i] = n + byte(i)
	}
	return id
}

// sampleMap is a populated map that exercises every field.
func sampleMap() *cluster.Map {
	return &cluster.Map{
		Version:   42,
		ClusterID: "cluster-7",
		Nodes: []cluster.NodeInfo{
			{ID: "n1", Addr: "10.0.0.1:9100", S3Addr: "10.0.0.1:9000"},
			{ID: "n2", Addr: "10.0.0.2:9100", S3Addr: "10.0.0.2:9000"},
		},
		Drives: []cluster.DriveLocation{
			{ID: drive(1), Node: "n1", Path: "/data/d1"},
			{ID: drive(2), Node: "n2", Path: "/data/d2"},
		},
		Sets: []cluster.SetInfo{
			{ID: 0, DataShards: 2, ParityShards: 2, Drives: []ids.DriveID{drive(1), drive(2), drive(3), drive(4)}},
			{ID: math.MaxUint32, DataShards: math.MaxInt32, ParityShards: 0, Drives: nil},
		},
		Partitions: []cluster.PartitionInfo{
			{ID: 0, Replicas: []ids.NodeID{"n1", "n2"}, NonVoters: []ids.NodeID{"n3"}},
			{ID: 1, Replicas: []ids.NodeID{"n2"}},
		},
	}
}

// normalize compares maps with nil and empty slices equal, the one
// normalization the conversion makes.
var normalize = cmpopts.EquateEmpty()

func TestRoundTripPreservesEveryField(t *testing.T) {
	cases := []struct {
		name string
		m    *cluster.Map
	}{
		{name: "empty", m: &cluster.Map{}},
		{name: "single node", m: &cluster.Map{Version: 1, ClusterID: "c", Nodes: []cluster.NodeInfo{{ID: "solo"}}}},
		{name: "populated", m: sampleMap()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ToProto(tc.m)
			if err != nil {
				t.Fatal(err)
			}
			got, err := FromProto(p)
			if err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(tc.m, got, normalize); d != "" {
				t.Fatalf("round trip differs:\n%s", d)
			}
		})
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	in := sampleMap()
	b, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(in, out, normalize); d != "" {
		t.Fatalf("wire round trip differs:\n%s", d)
	}
}

// randomMap builds a map from r whose values stay within what a Map may hold.
func randomMap(r *rand.Rand) *cluster.Map {
	name := func() ids.NodeID { return ids.NodeID("node-" + string(rune('a'+r.Intn(26)))) }
	drv := func() ids.DriveID {
		var id ids.DriveID
		r.Read(id[:])
		return id
	}
	m := &cluster.Map{Version: r.Uint64(), ClusterID: "rand"}
	for range r.Intn(5) {
		m.Nodes = append(m.Nodes, cluster.NodeInfo{ID: name(), Addr: "a", S3Addr: "s"})
	}
	for range r.Intn(6) {
		m.Drives = append(m.Drives, cluster.DriveLocation{ID: drv(), Node: name(), Path: "/p"})
	}
	for i := range r.Intn(4) {
		var drives []ids.DriveID
		for range r.Intn(8) {
			drives = append(drives, drv())
		}
		m.Sets = append(m.Sets, cluster.SetInfo{
			ID:           ids.SetID(i),
			DataShards:   r.Intn(math.MaxInt32),
			ParityShards: r.Intn(math.MaxInt32),
			Drives:       drives,
		})
	}
	for i := range r.Intn(5) {
		var reps, nvs []ids.NodeID
		for range r.Intn(4) {
			reps = append(reps, name())
		}
		for range r.Intn(2) {
			nvs = append(nvs, name())
		}
		m.Partitions = append(m.Partitions, cluster.PartitionInfo{ID: ids.PartitionID(i), Replicas: reps, NonVoters: nvs})
	}
	return m
}

func TestRandomRoundTrips(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := range 300 {
		in := randomMap(r)
		b, err := Marshal(in)
		if err != nil {
			t.Fatalf("iteration %d: marshal: %v", i, err)
		}
		out, err := Unmarshal(b)
		if err != nil {
			t.Fatalf("iteration %d: unmarshal: %v", i, err)
		}
		if d := cmp.Diff(in, out, normalize); d != "" {
			t.Fatalf("iteration %d differs:\n%s", i, d)
		}
	}
}

func TestToProtoRejectsNegativeShardCounts(t *testing.T) {
	cases := []struct {
		name         string
		data, parity int
	}{
		{name: "negative data", data: -1, parity: 2},
		{name: "negative parity", data: 2, parity: -1},
		{name: "data over uint32", data: math.MaxUint32 + 1, parity: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &cluster.Map{Sets: []cluster.SetInfo{{ID: 1, DataShards: tc.data, ParityShards: tc.parity}}}
			if _, err := ToProto(m); err == nil {
				t.Fatal("unrepresentable shard counts accepted")
			}
		})
	}
}

func TestFromProtoRejectsMalformedValues(t *testing.T) {
	cases := []struct {
		name string
		p    *uniov1.ClusterMap
	}{
		{name: "drive id too short", p: &uniov1.ClusterMap{Drives: []*uniov1.DriveLocation{{Id: make([]byte, 15)}}}},
		{name: "drive id too long", p: &uniov1.ClusterMap{Drives: []*uniov1.DriveLocation{{Id: make([]byte, 17)}}}},
		{name: "empty drive id", p: &uniov1.ClusterMap{Drives: []*uniov1.DriveLocation{{}}}},
		{name: "set drive id malformed", p: &uniov1.ClusterMap{Sets: []*uniov1.SetInfo{{Drives: [][]byte{make([]byte, 3)}}}}},
		{name: "shard count over int32", p: &uniov1.ClusterMap{Sets: []*uniov1.SetInfo{{DataShards: math.MaxUint32}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := FromProto(tc.p); err == nil {
				t.Fatal("malformed cluster map accepted")
			}
		})
	}
}

func TestUnmarshalRejectsGarbage(t *testing.T) {
	if _, err := Unmarshal([]byte{0xff, 0xff, 0xff}); err == nil {
		t.Fatal("garbage bytes accepted")
	}
}

func FuzzUnmarshal(f *testing.F) {
	b, err := Marshal(sampleMap())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Unmarshal(data)
		if err != nil {
			return
		}
		// Anything that decodes must re-encode and decode to the same map.
		again, err := Marshal(m)
		if err != nil {
			t.Fatalf("decoded map does not re-encode: %v", err)
		}
		out, err := Unmarshal(again)
		if err != nil {
			t.Fatalf("re-encoded map does not decode: %v", err)
		}
		if d := cmp.Diff(m, out, normalize); d != "" {
			t.Fatalf("re-encoding changed the map:\n%s", d)
		}
	})
}

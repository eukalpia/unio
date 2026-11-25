package topology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

// node returns a NodeInfo with a usable address.
func node(id string) cluster.NodeInfo {
	return cluster.NodeInfo{ID: ids.NodeID(id), Addr: id + ":9100", S3Addr: id + ":9000"}
}

// drivesOn returns count drives on node, with ids derived from the node name
// and the index so that they are unique across calls.
func drivesOn(node string, count int, base byte) []cluster.DriveLocation {
	var out []cluster.DriveLocation
	for i := range count {
		var id ids.DriveID
		copy(id[:], node)
		id[15] = base + byte(i)
		out = append(out, cluster.DriveLocation{ID: id, Node: ids.NodeID(node), Path: "/" + node})
	}
	return out
}

// checkLayout asserts every invariant of an initial layout.
func checkLayout(t *testing.T, m *cluster.Map, nodes []cluster.NodeInfo, drives []cluster.DriveLocation, setSize, parity, partitions int) {
	t.Helper()
	n := len(nodes)
	nodeOf := map[ids.DriveID]ids.NodeID{}
	for _, d := range drives {
		nodeOf[d.ID] = d.Node
	}

	if m.Version != 1 {
		t.Fatalf("version = %d, want 1", m.Version)
	}
	if len(m.Sets) != len(drives)/setSize {
		t.Fatalf("%d sets for %d drives of size %d", len(m.Sets), len(drives), setSize)
	}

	capPerNode := (setSize + n - 1) / n
	placed := map[ids.DriveID]int{}
	for _, s := range m.Sets {
		if s.DataShards != setSize-parity || s.ParityShards != parity {
			t.Fatalf("set %d shape %d+%d, want %d+%d", s.ID, s.DataShards, s.ParityShards, setSize-parity, parity)
		}
		if len(s.Drives) != setSize {
			t.Fatalf("set %d has %d drives, want %d", s.ID, len(s.Drives), setSize)
		}
		perNode := map[ids.NodeID]int{}
		for _, d := range s.Drives {
			owner, ok := nodeOf[d]
			if !ok {
				t.Fatalf("set %d names unknown drive %s", s.ID, d)
			}
			placed[d]++
			perNode[owner]++
		}
		for owner, c := range perNode {
			if c > capPerNode {
				t.Fatalf("set %d puts %d drives on %s, limit %d", s.ID, c, owner, capPerNode)
			}
		}
	}
	for _, d := range drives {
		if placed[d.ID] != 1 {
			t.Fatalf("drive %s placed %d times, want exactly once", d.ID, placed[d.ID])
		}
	}

	if len(m.Partitions) != partitions+1 {
		t.Fatalf("%d partitions, want system + %d", len(m.Partitions), partitions)
	}
	all := map[ids.NodeID]bool{}
	for _, nd := range nodes {
		all[nd.ID] = true
	}
	system := m.Partitions[0]
	voters := min(n, 5)
	if len(system.Replicas) != voters {
		t.Fatalf("system voters = %d, want %d", len(system.Replicas), voters)
	}
	if len(system.Replicas)+len(system.NonVoters) != n {
		t.Fatalf("system group covers %d nodes, want %d", len(system.Replicas)+len(system.NonVoters), n)
	}
	seen := map[ids.NodeID]bool{}
	for _, v := range append(append([]ids.NodeID{}, system.Replicas...), system.NonVoters...) {
		if !all[v] || seen[v] {
			t.Fatalf("system group member %q unknown or repeated", v)
		}
		seen[v] = true
	}
	for i := 1; i <= partitions; i++ {
		p := m.Partitions[i]
		if p.ID != ids.PartitionID(i) {
			t.Fatalf("partition %d has id %d", i, p.ID)
		}
		if len(p.Replicas) != min(3, n) || len(p.NonVoters) != 0 {
			t.Fatalf("partition %d has %d replicas and %d non-voters", i, len(p.Replicas), len(p.NonVoters))
		}
		distinct := map[ids.NodeID]bool{}
		for _, r := range p.Replicas {
			if !all[r] || distinct[r] {
				t.Fatalf("partition %d replica %q unknown or repeated", i, r)
			}
			distinct[r] = true
		}
	}
}

// nodeLoad counts partition replicas per node across the data partitions.
func nodeLoad(m *cluster.Map) map[ids.NodeID]int {
	load := map[ids.NodeID]int{}
	for _, p := range m.Partitions[1:] {
		for _, r := range p.Replicas {
			load[r]++
		}
	}
	return load
}

// sevenNodeDrives gives seven nodes four drives each: 28 drives, which fills
// seven sets of four, each on four different nodes.
func sevenNodeDrives() []cluster.DriveLocation {
	var out []cluster.DriveLocation
	for i := range 7 {
		out = append(out, drivesOn(fmt.Sprintf("n%d", i+1), 4, 0)...)
	}
	return out
}

func TestBuildInitialMapInvariants(t *testing.T) {
	cases := []struct {
		name       string
		nodes      []cluster.NodeInfo
		drives     []cluster.DriveLocation
		setSize    int
		parity     int
		partitions int
	}{
		{
			name:       "one node one drive (development)",
			nodes:      []cluster.NodeInfo{node("solo")},
			drives:     drivesOn("solo", 1, 0),
			setSize:    1,
			parity:     0,
			partitions: 4,
		},
		{
			name:       "one node eight drives",
			nodes:      []cluster.NodeInfo{node("solo")},
			drives:     drivesOn("solo", 8, 0),
			setSize:    4,
			parity:     2,
			partitions: 16,
		},
		{
			name:       "three nodes, even drives",
			nodes:      []cluster.NodeInfo{node("n1"), node("n2"), node("n3")},
			drives:     append(append(drivesOn("n1", 2, 0), drivesOn("n2", 2, 0)...), drivesOn("n3", 2, 0)...),
			setSize:    6,
			parity:     2,
			partitions: 8,
		},
		{
			name:       "three nodes, sets spread across nodes",
			nodes:      []cluster.NodeInfo{node("n1"), node("n2"), node("n3")},
			drives:     append(append(drivesOn("n1", 4, 0), drivesOn("n2", 4, 0)...), drivesOn("n3", 4, 0)...),
			setSize:    4,
			parity:     2,
			partitions: 16,
		},
		{
			name:       "seven nodes exceed system voters",
			nodes:      []cluster.NodeInfo{node("n1"), node("n2"), node("n3"), node("n4"), node("n5"), node("n6"), node("n7")},
			drives:     sevenNodeDrives(),
			setSize:    4,
			parity:     2,
			partitions: 16,
		},
		{
			name:       "two nodes keep two replicas",
			nodes:      []cluster.NodeInfo{node("n1"), node("n2")},
			drives:     append(drivesOn("n1", 4, 0), drivesOn("n2", 4, 0)...),
			setSize:    4,
			parity:     1,
			partitions: 2,
		},
		{
			name:       "sixteen drive sets",
			nodes:      []cluster.NodeInfo{node("n1"), node("n2"), node("n3"), node("n4")},
			drives:     append(append(drivesOn("n1", 8, 0), drivesOn("n2", 8, 0)...), append(drivesOn("n3", 8, 0), drivesOn("n4", 8, 0)...)...),
			setSize:    16,
			parity:     4,
			partitions: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := BuildInitialMap("cluster-x", tc.nodes, tc.drives, tc.setSize, tc.parity, tc.partitions)
			if err != nil {
				t.Fatal(err)
			}
			checkLayout(t, m, tc.nodes, tc.drives, tc.setSize, tc.parity, tc.partitions)
		})
	}
}

func TestReplicasAreSpreadEvenly(t *testing.T) {
	nodes := []cluster.NodeInfo{node("n1"), node("n2"), node("n3"), node("n4")}
	var drives []cluster.DriveLocation
	for _, nd := range nodes {
		drives = append(drives, drivesOn(string(nd.ID), 4, 0)...)
	}
	m, err := BuildInitialMap("c", nodes, drives, 4, 2, 16)
	if err != nil {
		t.Fatal(err)
	}
	load := nodeLoad(m)
	// 16 partitions with 3 replicas across 4 nodes: 12 replicas each.
	for _, nd := range nodes {
		if load[nd.ID] != 12 {
			t.Fatalf("node %s holds %d partition replicas, want 12", nd.ID, load[nd.ID])
		}
	}
}

func TestBuildIsDeterministicAcrossInputOrder(t *testing.T) {
	nodes := []cluster.NodeInfo{node("n3"), node("n1"), node("n2")}
	drives := append(append(drivesOn("n1", 4, 0), drivesOn("n2", 4, 0)...), drivesOn("n3", 4, 0)...)
	a, err := BuildInitialMap("c", nodes, drives, 4, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	// Reverse the inputs: the layout must not depend on the order.
	rn := []cluster.NodeInfo{nodes[2], nodes[1], nodes[0]}
	rd := make([]cluster.DriveLocation, len(drives))
	for i := range drives {
		rd[len(drives)-1-i] = drives[i]
	}
	b, err := BuildInitialMap("c", rn, rd, 4, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(a, b, cmpopts.EquateEmpty()); d != "" {
		t.Fatalf("layout depends on input order:\n%s", d)
	}
}

func TestBuildInitialMapRejectsBadInput(t *testing.T) {
	base := func() ([]cluster.NodeInfo, []cluster.DriveLocation) {
		return []cluster.NodeInfo{node("n1"), node("n2")}, append(drivesOn("n1", 2, 0), drivesOn("n2", 2, 0)...)
	}
	cases := []struct {
		name       string
		clusterID  string
		mutate     func(nodes []cluster.NodeInfo, drives []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation)
		setSize    int
		parity     int
		partitions int
		wantSubstr string
	}{
		{name: "empty cluster id", clusterID: "", setSize: 4, parity: 2, partitions: 4, wantSubstr: "cluster id"},
		{name: "no nodes", clusterID: "c", mutate: func(_ []cluster.NodeInfo, d []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation) {
			return nil, d
		}, setSize: 4, parity: 2, partitions: 4, wantSubstr: "node"},
		{name: "duplicate node", clusterID: "c", mutate: func(n []cluster.NodeInfo, d []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation) {
			return append(n, n[0]), d
		}, setSize: 4, parity: 2, partitions: 4, wantSubstr: "duplicate"},
		{name: "invalid node id", clusterID: "c", mutate: func(n []cluster.NodeInfo, d []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation) {
			n[0].ID = "bad/id"
			return n, d
		}, setSize: 4, parity: 2, partitions: 4, wantSubstr: "node id"},
		{name: "missing address", clusterID: "c", mutate: func(n []cluster.NodeInfo, d []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation) {
			n[1].Addr = ""
			return n, d
		}, setSize: 4, parity: 2, partitions: 4, wantSubstr: "address"},
		{name: "drive on unknown node", clusterID: "c", mutate: func(n []cluster.NodeInfo, d []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation) {
			d[0].Node = "ghost"
			return n, d
		}, setSize: 4, parity: 2, partitions: 4, wantSubstr: "unknown node"},
		{name: "zero drive id", clusterID: "c", mutate: func(n []cluster.NodeInfo, d []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation) {
			d[0].ID = ids.DriveID{}
			return n, d
		}, setSize: 4, parity: 2, partitions: 4, wantSubstr: "zero"},
		{name: "duplicate drive id", clusterID: "c", mutate: func(n []cluster.NodeInfo, d []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation) {
			d[1].ID = d[0].ID
			return n, d
		}, setSize: 4, parity: 2, partitions: 4, wantSubstr: "duplicate"},
		{name: "drives not a multiple of set size", clusterID: "c", mutate: func(n []cluster.NodeInfo, d []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation) {
			return n, d[:3]
		}, setSize: 4, parity: 2, partitions: 4, wantSubstr: "multiple"},
		{name: "fewer drives than one set", clusterID: "c", mutate: func(n []cluster.NodeInfo, d []cluster.DriveLocation) ([]cluster.NodeInfo, []cluster.DriveLocation) {
			return n, d[:2]
		}, setSize: 8, parity: 2, partitions: 4, wantSubstr: "multiple"},
		{name: "set size two", clusterID: "c", setSize: 2, parity: 1, partitions: 4, wantSubstr: "set size"},
		{name: "set size seventeen", clusterID: "c", setSize: 17, parity: 2, partitions: 4, wantSubstr: "set size"},
		{name: "parity zero in erasure set", clusterID: "c", setSize: 4, parity: 0, partitions: 4, wantSubstr: "parity"},
		{name: "parity above half", clusterID: "c", setSize: 4, parity: 3, partitions: 4, wantSubstr: "parity"},
		{name: "no partitions", clusterID: "c", setSize: 4, parity: 2, partitions: 0, wantSubstr: "partitions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes, drives := base()
			if tc.mutate != nil {
				nodes, drives = tc.mutate(nodes, drives)
			}
			_, err := BuildInitialMap(tc.clusterID, nodes, drives, tc.setSize, tc.parity, tc.partitions)
			if err == nil {
				t.Fatal("invalid input accepted")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestBuildRejectsInfeasibleSpread(t *testing.T) {
	// Node n1 holds six drives but a set of four may place at most two drives
	// on each of two nodes. Two sets can absorb only four of n1's drives.
	nodes := []cluster.NodeInfo{node("n1"), node("n2")}
	drives := append(drivesOn("n1", 6, 0), drivesOn("n2", 2, 0)...)
	if _, err := BuildInitialMap("c", nodes, drives, 4, 2, 4); err == nil {
		t.Fatal("layout accepted a node holding more drives than its share allows")
	}
}

func TestRandomLayoutsSatisfyInvariants(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	built := 0
	for range 200 {
		n := 1 + r.Intn(5)
		var nodes []cluster.NodeInfo
		var drives []cluster.DriveLocation
		for i := range n {
			name := "node" + string(rune('a'+i))
			nodes = append(nodes, node(name))
			drives = append(drives, drivesOn(name, 1+r.Intn(6), 0)...)
		}
		setSize := []int{4, 6, 8}[r.Intn(3)]
		parity := 1 + r.Intn(setSize/2)
		if len(drives) < setSize {
			continue
		}
		drives = drives[:len(drives)-len(drives)%setSize]
		partitions := 1 + r.Intn(8)
		m, err := BuildInitialMap("rand", nodes, drives, setSize, parity, partitions)
		if err != nil {
			// Some random spreads are infeasible; those must fail, not corrupt.
			continue
		}
		built++
		checkLayout(t, m, nodes, drives, setSize, parity, partitions)
	}
	if built < 20 {
		t.Fatalf("only %d random layouts were feasible; the generator is too strict", built)
	}
}

func TestSystemGroupMembersAreSorted(t *testing.T) {
	nodes := []cluster.NodeInfo{node("n3"), node("n1"), node("n2")}
	drives := append(append(drivesOn("n1", 4, 0), drivesOn("n2", 4, 0)...), drivesOn("n3", 4, 0)...)
	m, err := BuildInitialMap("c", nodes, drives, 4, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	voters := make([]string, 0, len(m.Partitions[0].Replicas))
	for _, v := range m.Partitions[0].Replicas {
		voters = append(voters, string(v))
	}
	if !sort.StringsAreSorted(voters) || !reflect.DeepEqual(voters, []string{"n1", "n2", "n3"}) {
		t.Fatalf("system voters = %v", voters)
	}
}

package topology

import (
	"bytes"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/cluster/mappb"
	"github.com/eukalpia/unio/internal/ids"
)

// testSpec is a four-node cluster with three drives on each node, laid out as
// three erasure sets of four drives with two parity shards, and eight data
// partitions. Drive paths live under root, so they are absolute on every OS.
func testSpec(root string) Spec {
	var nodes []NodeSpec
	for i := range 4 {
		name := fmt.Sprintf("n%d", i+1)
		var drives []string
		for d := range 3 {
			drives = append(drives, filepath.Join(root, name, fmt.Sprintf("d%d", d)))
		}
		nodes = append(nodes, NodeSpec{
			ID:     ids.NodeID(name),
			Addr:   name + ":9100",
			S3Addr: name + ":9000",
			Drives: drives,
		})
	}
	return Spec{ClusterID: "cluster-a", Nodes: nodes, SetSize: 4, Parity: 2, Partitions: 8}
}

// shuffled returns a copy of s with its nodes and each node's drives in a
// random order.
func shuffled(s Spec, rng *rand.Rand) Spec {
	out := s
	out.Nodes = append([]NodeSpec(nil), s.Nodes...)
	rng.Shuffle(len(out.Nodes), func(i, j int) {
		out.Nodes[i], out.Nodes[j] = out.Nodes[j], out.Nodes[i]
	})
	for i := range out.Nodes {
		drives := append([]string(nil), out.Nodes[i].Drives...)
		rng.Shuffle(len(drives), func(a, b int) {
			drives[a], drives[b] = drives[b], drives[a]
		})
		out.Nodes[i].Drives = drives
	}
	return out
}

// encodedMap builds the map for s and returns its stored encoding.
func encodedMap(t *testing.T, s Spec) []byte {
	t.Helper()
	m, err := MapFromSpec(s)
	if err != nil {
		t.Fatal(err)
	}
	b, err := mappb.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMapFromSpecIsIndependentOfOrder(t *testing.T) {
	spec := testSpec(t.TempDir())
	want := encodedMap(t, spec)
	rng := rand.New(rand.NewSource(1))
	for i := range 500 {
		got := encodedMap(t, shuffled(spec, rng))
		if !bytes.Equal(got, want) {
			t.Fatalf("permutation %d gives a different map", i)
		}
	}
}

func TestMapFromSpecDerivesDriveIDs(t *testing.T) {
	spec := testSpec(t.TempDir())
	m, err := MapFromSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Drives) != 12 || len(m.Sets) != 3 {
		t.Fatalf("%d drives in %d sets, want 12 in 3", len(m.Drives), len(m.Sets))
	}
	if m.ClusterID != spec.ClusterID {
		t.Fatalf("cluster id %q, want %q", m.ClusterID, spec.ClusterID)
	}
	for _, d := range m.Drives {
		if want := DriveIDFor(spec.ClusterID, d.Node, d.Path); d.ID != want {
			t.Fatalf("drive at %s on %s has id %s, want %s", d.Path, d.Node, d.ID, want)
		}
	}
}

func TestMapFromSpecStoresCleanPaths(t *testing.T) {
	root := t.TempDir()
	clean := testSpec(root)
	dirty := testSpec(root)
	for i := range dirty.Nodes {
		for j, p := range dirty.Nodes[i].Drives {
			dirty.Nodes[i].Drives[j] = p + string(filepath.Separator)
		}
	}
	if !bytes.Equal(encodedMap(t, clean), encodedMap(t, dirty)) {
		t.Fatal("a trailing separator changed the cluster map")
	}
}

func TestMapFromSpecAllowsSamePathOnTwoNodes(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "disk")
	spec := Spec{
		ClusterID: "cluster-a",
		Nodes: []NodeSpec{
			{ID: "n1", Addr: "n1:9100", S3Addr: "n1:9000", Drives: []string{shared, filepath.Join(root, "d1")}},
			{ID: "n2", Addr: "n2:9100", S3Addr: "n2:9000", Drives: []string{shared, filepath.Join(root, "d2")}},
		},
		SetSize: 4, Parity: 2, Partitions: 1,
	}
	m, err := MapFromSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Drives) != 4 || m.Drives[0].ID == m.Drives[1].ID {
		t.Fatal("the same path on two nodes must give two drives")
	}
}

func TestMapFromSpecRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *Spec)
		want   string
	}{
		{
			name:   "empty cluster id",
			mutate: func(s *Spec) { s.ClusterID = "" },
			want:   "cluster id",
		},
		{
			name:   "duplicate node id",
			mutate: func(s *Spec) { s.Nodes[1].ID = s.Nodes[0].ID },
			want:   "duplicate node",
		},
		{
			name: "duplicate drive path on one node",
			mutate: func(s *Spec) {
				d := s.Nodes[0].Drives
				d[1] = d[0] + string(filepath.Separator)
			},
			want: "more than once",
		},
		{
			name:   "relative drive path",
			mutate: func(s *Spec) { s.Nodes[0].Drives[0] = filepath.Join("relative", "d0") },
			want:   "not absolute",
		},
		{
			name:   "empty drive path",
			mutate: func(s *Spec) { s.Nodes[0].Drives[0] = "" },
			want:   "not absolute",
		},
		{
			name:   "node with no drives",
			mutate: func(s *Spec) { s.Nodes[3].Drives = nil },
			want:   "n4 has no drives",
		},
		{
			name:   "invalid node id",
			mutate: func(s *Spec) { s.Nodes[0].ID = "Node_1" },
			want:   "not a DNS label",
		},
		{
			name:   "no nodes",
			mutate: func(s *Spec) { s.Nodes = nil },
			want:   "at least one node",
		},
		{
			name:   "parity outside the set shape",
			mutate: func(s *Spec) { s.Parity = 3 },
			want:   "parity",
		},
		{
			name:   "no partitions",
			mutate: func(s *Spec) { s.Partitions = 0 },
			want:   "partitions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := testSpec(t.TempDir())
			tc.mutate(&spec)
			_, err := MapFromSpec(spec)
			if err == nil {
				t.Fatal("accepted an invalid spec")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

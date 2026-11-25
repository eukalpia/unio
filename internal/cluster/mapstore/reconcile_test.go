package mapstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

func TestReconcileSavesWhenNothingIsStored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "meta")
	want := testMap()
	got, err := Reconcile(dir, want)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("Reconcile did not return the configured map when nothing was stored")
	}
	stored, err := Load(dir)
	if err != nil {
		t.Fatalf("map was not saved: %v", err)
	}
	if !sameBytes(t, stored, want) {
		t.Fatal("saved map differs from the configured one")
	}
}

func TestReconcileReturnsTheStoredMapWhenEqual(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	before := readStored(t, dir)
	want := testMap()
	got, err := Reconcile(dir, want)
	if err != nil {
		t.Fatal(err)
	}
	if got == want {
		t.Fatal("Reconcile returned the configured map instead of the stored one")
	}
	if !sameBytes(t, got, want) {
		t.Fatal("returned map differs from the configured one")
	}
	if after := readStored(t, dir); string(after) != string(before) {
		t.Fatal("Reconcile rewrote an equal map")
	}
}

// TestReconcileRefusesADifferentLayout checks each branch of the mismatch
// report. Each case changes one part of the configured map and expects the
// first difference to be named. The stored map must be left alone.
func TestReconcileRefusesADifferentLayout(t *testing.T) {
	extraDrive := cluster.DriveLocation{ID: testDrive(9), Node: "n1", Path: "/data/d9"}
	cases := []struct {
		name   string
		mutate func(m *cluster.Map)
		want   string
	}{
		{
			name:   "node removed",
			mutate: func(m *cluster.Map) { m.Nodes = m.Nodes[:2] },
			want:   "node set differs: stored has n1, n2, n3, configured has n1, n2",
		},
		{
			name:   "node address changed",
			mutate: func(m *cluster.Map) { m.Nodes[1].Addr = "n9:9100" },
			want:   "node n2 address differs: stored has n2:9100",
		},
		{
			name:   "node listed twice",
			mutate: func(m *cluster.Map) { m.Nodes = append(m.Nodes, m.Nodes[2]) },
			want:   "node count differs: stored has 3 entries, configured has 4",
		},
		{
			name:   "cluster id changed",
			mutate: func(m *cluster.Map) { m.ClusterID = "cluster-b" },
			want:   `cluster id differs: stored has "cluster-a", configured has "cluster-b"`,
		},
		{
			name:   "map version changed",
			mutate: func(m *cluster.Map) { m.Version = 2 },
			want:   "map version differs: stored has 1, configured has 2",
		},
		{
			name:   "drive moved to another node",
			mutate: func(m *cluster.Map) { m.Drives[0].Node = "n2" },
			want:   "in the stored map but on node n2 at /data/d1 in the configured one",
		},
		{
			name:   "drive removed",
			mutate: func(m *cluster.Map) { m.Drives = m.Drives[1:] },
			want:   "/data/d1 is in the stored map only",
		},
		{
			name:   "drive added",
			mutate: func(m *cluster.Map) { m.Drives = append(m.Drives, extraDrive) },
			want:   "/data/d9 is in the configured map only",
		},
		{
			name:   "erasure set count changed",
			mutate: func(m *cluster.Map) { m.Sets = m.Sets[:1] },
			want:   "erasure set count differs: stored has 2, configured has 1",
		},
		{
			name: "erasure set shape changed",
			mutate: func(m *cluster.Map) {
				m.Sets[1].DataShards, m.Sets[1].ParityShards = 1, 2
			},
			want: "erasure set 1 differs: stored shape is 2+1, configured is 1+2",
		},
		{
			name: "erasure set drives reordered",
			mutate: func(m *cluster.Map) {
				d := m.Sets[1].Drives
				d[0], d[1] = d[1], d[0]
			},
			want: "erasure set 1 differs: slot 0 is drive",
		},
		{
			name:   "erasure set drive count changed",
			mutate: func(m *cluster.Map) { m.Sets[0].Drives = m.Sets[0].Drives[:2] },
			want:   "erasure set 0 differs: stored has 3 drives, configured has 2",
		},
		{
			name:   "partition count changed",
			mutate: func(m *cluster.Map) { m.Partitions = m.Partitions[:4] },
			want:   "partition count differs: stored 4, configured 3",
		},
		{
			name:   "partition replicas reordered",
			mutate: func(m *cluster.Map) { m.Partitions[2].Replicas = []ids.NodeID{"n3", "n2", "n1"} },
			want:   "partition 2 differs: stored replicas are [n2,n3,n1], configured are [n3,n2,n1]",
		},
		{
			name:   "system group gains a non-voter",
			mutate: func(m *cluster.Map) { m.Partitions[0].NonVoters = []ids.NodeID{"n4"} },
			want:   "system group differs: stored non-voters are [], configured are [n4]",
		},
		{
			name: "the first difference is named",
			mutate: func(m *cluster.Map) {
				m.Nodes = m.Nodes[:2]
				m.Partitions = m.Partitions[:2]
			},
			want: "node set differs",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := Save(dir, testMap()); err != nil {
				t.Fatal(err)
			}
			before := readStored(t, dir)

			want := cloneMap(t, testMap())
			tc.mutate(want)
			got, err := Reconcile(dir, want)
			if err == nil {
				t.Fatalf("Reconcile accepted a different layout: %+v", got)
			}
			if !errors.Is(err, ErrMismatch) {
				t.Fatalf("error %v does not wrap ErrMismatch", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
			if !strings.HasSuffix(err.Error(), layoutHint) {
				t.Fatalf("error %q does not end with the layout hint", err)
			}
			if !strings.Contains(err.Error(), "not supported in v0.1") {
				t.Fatalf("error %q does not say that layout changes are unsupported", err)
			}
			if after := readStored(t, dir); string(after) != string(before) {
				t.Fatal("a refused Reconcile changed the stored map")
			}
		})
	}
}

func TestReconcileKeepsACorruptMap(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	damaged := readStored(t, dir)
	damaged[len(damaged)-1] ^= 0xff
	writeStored(t, dir, damaged)

	got, err := Reconcile(dir, testMap())
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("error %v, want ErrCorrupt", err)
	}
	if errors.Is(err, ErrMismatch) {
		t.Fatal("corruption reported as a mismatch")
	}
	if got != nil {
		t.Fatal("Reconcile returned a map for a corrupt file")
	}
	if after := readStored(t, dir); string(after) != string(damaged) {
		t.Fatal("Reconcile overwrote a corrupt map")
	}
}

func TestReconcileRejectsNilAndEmptyDir(t *testing.T) {
	if _, err := Reconcile(t.TempDir(), nil); err == nil {
		t.Fatal("Reconcile accepted a nil map")
	}
	if _, err := Reconcile("", testMap()); err == nil {
		t.Fatal("Reconcile accepted an empty directory")
	}
}

func TestReconcileCleansStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	// A map is already stored, so Reconcile takes the equal branch and never
	// reaches Save. The leftover from an earlier crash must still go.
	stray := filepath.Join(dir, tempPrefix+"77")
	if err := os.WriteFile(stray, []byte("torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stray temp file survived Reconcile: %v", err)
	}
	assertOnlyMapFile(t, dir)
}

func TestReconcileRemovesStaleTempBeforeSaving(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, tempPrefix+"78")
	if err := os.WriteFile(stray, []byte("torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	assertOnlyMapFile(t, dir)
}

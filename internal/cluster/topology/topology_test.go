package topology

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/clustertest"
	"github.com/eukalpia/unio/internal/cluster/remotedrive"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// twoNodeMap places one drive on each of nodes a and b.
func twoNodeMap(t *testing.T, version uint64) (*cluster.Map, ids.DriveID, ids.DriveID) {
	t.Helper()
	nodes := []cluster.NodeInfo{node("a"), node("b")}
	drives := append(drivesOn("a", 1, 0), drivesOn("b", 1, 0)...)
	m, err := BuildInitialMap("c", nodes, drives, 1, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = version
	return m, m.Drives[0].ID, m.Drives[1].ID
}

// findOn returns the drive of the map placed on node.
func findOn(m *cluster.Map, node ids.NodeID) ids.DriveID {
	for _, d := range m.Drives {
		if d.Node == node {
			return d.ID
		}
	}
	return ids.DriveID{}
}

func TestNewValidatesConfig(t *testing.T) {
	c := clustertest.NewCluster(t, "a")
	m, _, _ := twoNodeMap(t, 1)
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "no local id", cfg: Config{Transport: c.Nodes["a"], Map: m}},
		{name: "no transport", cfg: Config{Local: "a", Map: m}},
		{name: "no map", cfg: Config{Local: "a", Transport: c.Nodes["a"]}},
		{name: "local not in map", cfg: Config{Local: "zz", Transport: c.Nodes["a"], Map: m}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestDriveResolution(t *testing.T) {
	c := clustertest.NewCluster(t, "a", "b")
	m, _, _ := twoNodeMap(t, 1)
	localID := findOn(m, "a")
	remoteID := findOn(m, "b")
	topo, err := New(Config{Local: "a", Transport: c.Nodes["a"], Map: m})
	if err != nil {
		t.Fatal(err)
	}

	local := clustertest.NewMemDrive(localID, "a", 0)
	if err := topo.AddDrive(local); err != nil {
		t.Fatal(err)
	}
	got, err := topo.Drive(localID)
	if err != nil {
		t.Fatal(err)
	}
	if got != drive.Drive(local) {
		t.Fatal("local drive does not resolve to the registered engine")
	}

	remote, err := topo.Drive(remoteID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := remote.(*remotedrive.Client); !ok {
		t.Fatalf("remote drive resolved to %T, want *remotedrive.Client", remote)
	}
	again, _ := topo.Drive(remoteID)
	if again != remote {
		t.Fatal("remote client is not cached")
	}

	if _, err := topo.Drive(ids.NewDriveID()); !errors.Is(err, ErrUnknownDrive) {
		t.Fatalf("unknown drive: %v", err)
	}
}

func TestLocalDriveMustBeRegistered(t *testing.T) {
	c := clustertest.NewCluster(t, "a")
	m, _, _ := twoNodeMap(t, 1)
	topo, err := New(Config{Local: "a", Transport: c.Nodes["a"], Map: m})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := topo.Drive(findOn(m, "a")); !errors.Is(err, drive.ErrOffline) {
		t.Fatalf("unregistered local drive: %v, want ErrOffline", err)
	}
	stranger := clustertest.NewMemDrive(ids.NewDriveID(), "a", 0)
	if err := topo.AddDrive(stranger); err == nil {
		t.Fatal("drive absent from the cluster map was accepted")
	}
	misplaced := clustertest.NewMemDrive(findOn(m, "b"), "a", 0)
	if err := topo.AddDrive(misplaced); err == nil {
		t.Fatal("drive placed on another node was accepted")
	}
}

func TestRemoteDriveReachesServedDrive(t *testing.T) {
	c := clustertest.NewCluster(t, "a", "b")
	m, _, _ := twoNodeMap(t, 1)
	remoteID := findOn(m, "b")
	topoA, err := New(Config{Local: "a", Transport: c.Nodes["a"], Map: m})
	if err != nil {
		t.Fatal(err)
	}
	topoB, err := New(Config{Local: "b", Transport: c.Nodes["b"], Map: m})
	if err != nil {
		t.Fatal(err)
	}
	served := clustertest.NewMemDrive(remoteID, "b", 0)
	if err := topoB.AddDrive(served); err != nil {
		t.Fatal(err)
	}
	srv := remotedrive.NewServer(remotedrive.ServerOptions{})
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Add(served); err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(c.Nodes["b"]); err != nil {
		t.Fatal(err)
	}

	d, err := topoA.Drive(remoteID)
	if err != nil {
		t.Fatal(err)
	}
	sid := ids.ShardID{Data: ids.NewDataID()}
	payload := []byte("over the wire")
	if err := d.WriteShard(context.Background(), sid, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if _, err := served.StatShard(context.Background(), sid); err != nil {
		t.Fatalf("shard missing on the serving node: %v", err)
	}
}

func TestPingAnswersWithIdentity(t *testing.T) {
	c := clustertest.NewCluster(t, "a", "b")
	m, _, _ := twoNodeMap(t, 9)
	if _, err := New(Config{Local: "b", Transport: c.Nodes["b"], Map: m}); err != nil {
		t.Fatal(err)
	}
	var resp uniov1.PingResponse
	if err := c.Nodes["a"].Call(context.Background(), "b", MethodPing, &uniov1.PingRequest{}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.GetNode() != "b" || resp.GetMapVersion() != 9 {
		t.Fatalf("ping answered %+v", &resp)
	}
}

func TestSetMapRefusesOlderVersions(t *testing.T) {
	c := clustertest.NewCluster(t, "a")
	m1, _, _ := twoNodeMap(t, 1)
	m3, _, _ := twoNodeMap(t, 3)
	m2, _, _ := twoNodeMap(t, 2)
	topo, err := New(Config{Local: "a", Transport: c.Nodes["a"], Map: m1})
	if err != nil {
		t.Fatal(err)
	}
	if err := topo.SetMap(m3); err != nil {
		t.Fatal(err)
	}
	if err := topo.SetMap(m2); err == nil {
		t.Fatal("older map replaced a newer one")
	}
	if topo.Map().Version != 3 {
		t.Fatalf("map version = %d, want 3", topo.Map().Version)
	}
	if err := topo.SetMap(nil); err == nil {
		t.Fatal("nil map accepted")
	}
}

func TestHealthTracksUnreachablePeers(t *testing.T) {
	c := clustertest.NewCluster(t, "a", "b")
	m, _, _ := twoNodeMap(t, 1)
	topo, err := New(Config{
		Local:            "a",
		Transport:        c.Nodes["a"],
		Map:              m,
		ProbeInterval:    10 * time.Millisecond,
		ProbeTimeout:     100 * time.Millisecond,
		FailureThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Local: "b", Transport: c.Nodes["b"], Map: m}); err != nil {
		t.Fatal(err)
	}
	if !topo.Healthy("a") || !topo.Healthy("b") {
		t.Fatal("nodes are not healthy before any probe")
	}
	if topo.Healthy("ghost") {
		t.Fatal("node outside the map reported healthy")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		topo.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Stop b. Its probes now fail, and after the threshold it is reported down.
	if err := c.Nodes["b"].Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(15 * time.Second)
	for topo.Healthy("b") {
		select {
		case <-deadline:
			t.Fatal("stopped peer still reported healthy")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !topo.Healthy("a") {
		t.Fatal("local node reported unhealthy")
	}
}

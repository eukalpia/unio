package clustertest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/uniwire"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// echo registers an echo handler on ep.
func echo(t *testing.T, ep cluster.RPC) {
	t.Helper()
	if err := ep.Register("test.Echo", func(_ context.Context, _ ids.NodeID, req []byte) ([]byte, error) {
		return req, nil
	}); err != nil {
		t.Fatal(err)
	}
}

// pair returns a network with endpoints a and b, b serving echo.
func pair(t *testing.T) (*FakeNetwork, *FakeRPC, *FakeRPC) {
	t.Helper()
	n := NewFakeNetwork()
	a := n.Endpoint("a")
	b := n.Endpoint("b")
	echo(t, b)
	return n, a, b
}

func TestFakeCallMarshalsAndReturns(t *testing.T) {
	_, a, _ := pair(t)
	var resp uniov1.DriveRequest
	if err := a.Call(context.Background(), "b", "test.Echo", &uniov1.DriveRequest{Drive: []byte("hi")}, &resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.GetDrive()) != "hi" {
		t.Fatalf("echo = %q", resp.GetDrive())
	}
}

func TestFakePartitionFailsUnavailableUntilHealed(t *testing.T) {
	n, a, _ := pair(t)
	n.Partition("a", "b")
	var resp uniov1.DriveRequest
	err := a.Call(context.Background(), "b", "test.Echo", &uniov1.DriveRequest{}, &resp)
	if !errors.Is(err, uniwire.ErrUnavailable) {
		t.Fatalf("partitioned call = %v, want ErrUnavailable", err)
	}
	n.Heal()
	if err := a.Call(context.Background(), "b", "test.Echo", &uniov1.DriveRequest{}, &resp); err != nil {
		t.Fatalf("call after heal: %v", err)
	}
}

func TestFakePartitionIsSymmetric(t *testing.T) {
	n, a, b := pair(t)
	echo(t, a)
	n.Partition("a", "b")
	var resp uniov1.DriveRequest
	if err := b.Call(context.Background(), "a", "test.Echo", &uniov1.DriveRequest{}, &resp); !errors.Is(err, uniwire.ErrUnavailable) {
		t.Fatalf("reverse direction = %v, want ErrUnavailable", err)
	}
}

func TestFakeIsolateCutsEveryLink(t *testing.T) {
	n, a, _ := pair(t)
	echo(t, n.Endpoint("c"))
	n.Isolate("b")
	var resp uniov1.DriveRequest
	if err := a.Call(context.Background(), "b", "test.Echo", &uniov1.DriveRequest{}, &resp); !errors.Is(err, uniwire.ErrUnavailable) {
		t.Fatalf("call to isolated node = %v", err)
	}
	if err := a.Call(context.Background(), "c", "test.Echo", &uniov1.DriveRequest{}, &resp); err != nil {
		t.Fatalf("call to a healthy node failed: %v", err)
	}
}

func TestFakeUnknownMethodAndNode(t *testing.T) {
	_, a, _ := pair(t)
	var resp uniov1.DriveRequest
	if err := a.Call(context.Background(), "b", "no.Such", &uniov1.DriveRequest{}, &resp); !errors.Is(err, uniwire.ErrUnknownMethod) {
		t.Fatalf("unknown method = %v", err)
	}
	if err := a.Call(context.Background(), "nobody", "test.Echo", &uniov1.DriveRequest{}, &resp); !errors.Is(err, uniwire.ErrUnavailable) {
		t.Fatalf("unknown node = %v", err)
	}
}

func TestFakeErrorsKeepSentinels(t *testing.T) {
	n := NewFakeNetwork()
	a := n.Endpoint("a")
	b := n.Endpoint("b")
	if err := b.Register("test.Fail", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		return nil, drive.ErrShardNotFound
	}); err != nil {
		t.Fatal(err)
	}
	var resp uniov1.DriveRequest
	if err := a.Call(context.Background(), "b", "test.Fail", &uniov1.DriveRequest{}, &resp); !errors.Is(err, drive.ErrShardNotFound) {
		t.Fatalf("error = %v, want the sentinel itself", err)
	}
}

func TestFakeLatencyHonoursContext(t *testing.T) {
	n, a, _ := pair(t)
	n.SetLatency(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var resp uniov1.DriveRequest
	start := time.Now()
	if err := a.Call(ctx, "b", "test.Echo", &uniov1.DriveRequest{}, &resp); !errors.Is(err, context.Canceled) {
		t.Fatalf("call under latency = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("latency ignored the canceled context")
	}
}

func TestFakeRegisterRejectsDuplicates(t *testing.T) {
	ep := NewFakeNetwork().Endpoint("a")
	echo(t, ep)
	if err := ep.Register("test.Echo", func(context.Context, ids.NodeID, []byte) ([]byte, error) { return nil, nil }); err == nil {
		t.Fatal("duplicate registration accepted")
	}
}

func TestFakeTopologyDrivesAndHealth(t *testing.T) {
	m := &cluster.Map{Version: 1}
	f := NewFakeTopology("a", m)
	if f.LocalNode() != "a" || f.Map() != m {
		t.Fatal("identity or map not returned")
	}
	id := ids.NewDriveID()
	if _, err := f.Drive(id); !errors.Is(err, drive.ErrOffline) {
		t.Fatalf("unset drive = %v, want ErrOffline", err)
	}
	d := NewMemDrive(id, "a", 0)
	f.SetDrive(d)
	got, err := f.Drive(id)
	if err != nil || got != drive.Drive(d) {
		t.Fatalf("drive = %v, %v", got, err)
	}
	if !f.Healthy("b") {
		t.Fatal("node reported down by default")
	}
	f.SetDown("b", true)
	if f.Healthy("b") {
		t.Fatal("node still healthy after SetDown")
	}
	f.SetDown("b", false)
	if !f.Healthy("b") {
		t.Fatal("node still down after recovery")
	}
}

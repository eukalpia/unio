package chaos

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	testContainerID = "0123456789ab"
	testNetwork     = "unio-4x4_internal"
)

// networkRunner answers `compose ps -q` with a container id and lets the
// test script the result of `docker network` commands.
func networkRunner(netErr func(argv []string) error) *fakeRunner {
	return &fakeRunner{reply: func(argv []string) ([]byte, error) {
		if argv[1] == "compose" {
			return []byte(testContainerID + "\n"), nil
		}
		if netErr != nil {
			return nil, netErr(argv)
		}
		return nil, nil
	}}
}

func TestNetworkName(t *testing.T) {
	c, err := NewCluster(&fakeRunner{}, filepath.Join(t.TempDir(), "c.yml"), testProject, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := NewNetwork(c).Name(); got != testNetwork {
		t.Fatalf("Name() = %q, want %q", got, testNetwork)
	}
}

func TestNetworkDisconnectAndConnect(t *testing.T) {
	tests := []struct {
		name string
		run  func(n *Network) error
		verb string
	}{
		{"disconnect", func(n *Network) error { return n.Disconnect(context.Background(), "node2") }, "disconnect"},
		{"connect", func(n *Network) error { return n.Connect(context.Background(), "node2") }, "connect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := networkRunner(nil)
			file := filepath.Join(t.TempDir(), "c.yml")
			c, err := NewCluster(fr, file, testProject, 4)
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.run(NewNetwork(c)); err != nil {
				t.Fatal(err)
			}
			calls := fr.argvs()
			if len(calls) != 2 {
				t.Fatalf("got %d commands, want 2 (ps, then network)", len(calls))
			}
			if diff := cmp.Diff(composeArgv(file, "ps", "-q", "node2"), calls[0]); diff != "" {
				t.Fatalf("resolve argv mismatch (-want +got):\n%s", diff)
			}
			want := []string{"docker", "network", tt.verb, testNetwork, testContainerID}
			if diff := cmp.Diff(want, calls[1]); diff != "" {
				t.Fatalf("network argv mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNetworkRejectsBadServiceBeforeDocker(t *testing.T) {
	fr := networkRunner(nil)
	c, err := NewCluster(fr, filepath.Join(t.TempDir(), "c.yml"), testProject, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewNetwork(c).Disconnect(context.Background(), "postgres"); err == nil {
		t.Fatal("non-node service accepted")
	}
	if n := len(fr.argvs()); n != 0 {
		t.Fatalf("%d command(s) ran for an invalid service", n)
	}
}

func TestPartitionCutAndHeal(t *testing.T) {
	fr := networkRunner(nil)
	c, err := NewCluster(fr, filepath.Join(t.TempDir(), "c.yml"), testProject, 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := NewPartition(NewNetwork(c), "node1")

	if err := p.Heal(ctx); err != nil {
		t.Fatalf("Heal before Cut: %v", err)
	}
	if n := len(fr.argvs()); n != 0 {
		t.Fatalf("Heal without a cut ran %d command(s)", n)
	}
	if err := p.Cut(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Cut(ctx); err == nil {
		t.Fatal("second Cut accepted")
	}
	if err := p.Heal(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Heal(ctx); err != nil {
		t.Fatalf("second Heal: %v", err)
	}
	var verbs []string
	for _, argv := range fr.argvs() {
		if argv[1] == "network" {
			verbs = append(verbs, argv[2])
		}
	}
	if diff := cmp.Diff([]string{"disconnect", "connect"}, verbs); diff != "" {
		t.Fatalf("network verbs mismatch (-want +got):\n%s", diff)
	}
}

func TestPartitionHealRetriesAfterFailure(t *testing.T) {
	connectFails := true
	fr := networkRunner(func(argv []string) error {
		if argv[2] == "connect" && connectFails {
			return errors.New("daemon busy")
		}
		return nil
	})
	c, err := NewCluster(fr, filepath.Join(t.TempDir(), "c.yml"), testProject, 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := NewPartition(NewNetwork(c), "node1")
	if err := p.Cut(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Heal(ctx); err == nil {
		t.Fatal("Heal reported success although connect failed")
	}
	connectFails = false
	if err := p.Heal(ctx); err != nil {
		t.Fatalf("retry Heal: %v", err)
	}
	if err := p.Heal(ctx); err != nil {
		t.Fatalf("Heal after recovery: %v", err)
	}
	connects := 0
	for _, argv := range fr.argvs() {
		if argv[1] == "network" && argv[2] == "connect" {
			connects++
		}
	}
	if connects != 2 {
		t.Fatalf("connect attempts = %d, want 2 (failed, then retried once)", connects)
	}
}

package raftstore_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/metatest"
	"github.com/eukalpia/unio/internal/meta/raftstore"
)

// TestOpenCreatesPrivateDataDirectory checks that the store creates its data
// directory readable only by its owner. Metadata lists every bucket and object
// key, so other local users must not be able to read it.
func TestOpenCreatesPrivateDataDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	const node = ids.NodeID("n1")
	cl := metatest.NewCluster(1)
	dir := filepath.Join(t.TempDir(), "metadata")
	s, err := raftstore.Open(context.Background(), raftstore.Config{
		Node:    node,
		Map:     buildMap([]ids.NodeID{node}, 1),
		DataDir: dir,
		RPC:     cl.RPC(node),
		Net:     cl.RaftNetwork(node),
		Options: raftstore.Options{HeartbeatTimeout: testHeartbeat},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat data directory: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("data directory mode %o, want 0700", perm)
	}
}

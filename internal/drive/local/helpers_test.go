package local

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// treeSnapshot maps every file and directory below root to a digest of its
// content, so that a test can prove an operation changed nothing on disk.
func treeSnapshot(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	snap := make(map[string][32]byte)
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if e.IsDir() {
			snap[rel+"/"] = [32]byte{}
			return nil
		}
		data, err := os.ReadFile(p) // #nosec G304 -- test-only walk of a temporary directory
		if err != nil {
			return err
		}
		snap[rel] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snap
}

// requireSameTree fails the test unless root holds exactly the snapshot want.
func requireSameTree(t *testing.T, root string, want map[string][32]byte) {
	t.Helper()
	got := treeSnapshot(t, root)
	if len(got) != len(want) {
		t.Fatalf("tree has %d entries, want %d", len(got), len(want))
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Fatalf("entry %q changed or vanished", k)
		}
	}
}

// formatBytes returns the raw format record of the drive at root.
func formatBytes(t *testing.T, root string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".unio", "format.pb")) // #nosec G304 -- test-only read of a temporary directory
	if err != nil {
		t.Fatalf("read format record: %v", err)
	}
	return data
}

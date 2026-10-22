package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFormatThenOpenKeepsDriveID(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	id, err := Format(ctx, dir, "cluster-a")
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if id.IsZero() {
		t.Fatal("format returned a zero drive id")
	}
	d, err := Open(ctx, dir, Options{ClusterID: "cluster-a"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if d.ID() != id {
		t.Fatalf("drive id %s, want %s", d.ID(), id)
	}
}

func TestFormatTwiceFails(t *testing.T) {
	dir := t.TempDir()
	if _, err := Format(context.Background(), dir, "c"); err != nil {
		t.Fatalf("first format: %v", err)
	}
	if _, err := Format(context.Background(), dir, "c"); !errors.Is(err, ErrAlreadyFormatted) {
		t.Fatalf("second format: err = %v, want ErrAlreadyFormatted", err)
	}
}

func TestFormatRequiresClusterID(t *testing.T) {
	if _, err := Format(context.Background(), t.TempDir(), ""); err == nil {
		t.Fatal("format without a cluster id succeeded")
	}
}

func TestOpenRefusesOtherCluster(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := Format(ctx, dir, "cluster-a"); err != nil {
		t.Fatalf("format: %v", err)
	}
	if _, err := Open(ctx, dir, Options{ClusterID: "cluster-b"}); !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("open with other cluster: err = %v, want ErrClusterMismatch", err)
	}
}

func TestOpenUnformattedFails(t *testing.T) {
	if _, err := Open(context.Background(), t.TempDir(), Options{}); !errors.Is(err, ErrNotFormatted) {
		t.Fatalf("open unformatted: err = %v, want ErrNotFormatted", err)
	}
}

func TestOpenRejectsCorruptFormatRecord(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := Format(ctx, dir, "c"); err != nil {
		t.Fatalf("format: %v", err)
	}
	path := filepath.Join(dir, ".unio", "format.pb")
	if err := os.WriteFile(path, []byte{0xff, 0x00, 0x13}, 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, err := Open(ctx, dir, Options{}); err == nil {
		t.Fatal("open accepted a corrupt format record")
	}
}

func TestFormatRecordIsWrittenAtomically(t *testing.T) {
	dir := t.TempDir()
	if _, err := Format(context.Background(), dir, "c"); err != nil {
		t.Fatalf("format: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".unio", "format.pb.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary format file left behind: %v", err)
	}
}

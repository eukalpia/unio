package local

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestOpenExpectedID(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	id := ids.NewDriveID()
	if err := FormatWithID(ctx, dir, "c", id); err != nil {
		t.Fatalf("format: %v", err)
	}
	tests := []struct {
		name     string
		expected ids.DriveID
		wantErr  bool
	}{
		{"zero expects nothing", ids.DriveID{}, false},
		{"matching id opens", id, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Open(ctx, dir, Options{ClusterID: "c", ExpectedID: tc.expected})
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer d.Close()
			if d.ID() != id {
				t.Fatalf("drive id %s, want %s", d.ID(), id)
			}
		})
	}
}

// TestOpenWrongExpectedIDLeavesDriveUntouched opens a drive that holds one
// identity while expecting another, as happens after a drive is moved to
// another slot. The open must fail, name both ids, and change no file.
func TestOpenWrongExpectedIDLeavesDriveUntouched(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	held := ids.NewDriveID()
	expected := ids.NewDriveID()
	if err := FormatWithID(ctx, dir, "c", held); err != nil {
		t.Fatalf("format: %v", err)
	}
	formatBefore := formatBytes(t, dir)
	treeBefore := treeSnapshot(t, dir)

	_, err := Open(ctx, dir, Options{ClusterID: "c", ExpectedID: expected})
	if !errors.Is(err, ErrDriveIDMismatch) {
		t.Fatalf("open: err = %v, want ErrDriveIDMismatch", err)
	}
	for _, want := range []string{held.String(), expected.String()} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name id %s", err, want)
		}
	}
	if got := formatBytes(t, dir); string(got) != string(formatBefore) {
		t.Fatal("format record bytes changed by a refused open")
	}
	requireSameTree(t, dir, treeBefore)
}

func TestOpenWrongClusterWinsOverExpectedID(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	id := ids.NewDriveID()
	if err := FormatWithID(ctx, dir, "c", id); err != nil {
		t.Fatalf("format: %v", err)
	}
	_, err := Open(ctx, dir, Options{ClusterID: "other", ExpectedID: ids.NewDriveID()})
	if !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("open: err = %v, want ErrClusterMismatch", err)
	}
}

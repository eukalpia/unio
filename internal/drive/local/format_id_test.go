package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestFormatWithIDWritesGivenID(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	want := ids.NewDriveID()
	if err := FormatWithID(ctx, dir, "cluster-a", want); err != nil {
		t.Fatalf("format with id: %v", err)
	}
	d, err := Open(ctx, dir, Options{ClusterID: "cluster-a", ExpectedID: want})
	if err != nil {
		t.Fatalf("open with the id it was formatted with: %v", err)
	}
	defer d.Close()
	if d.ID() != want {
		t.Fatalf("drive id %s, want %s", d.ID(), want)
	}
}

func TestFormatWithIDRejectsInvalidArguments(t *testing.T) {
	ctx := context.Background()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	tests := []struct {
		name      string
		ctx       context.Context
		clusterID string
		id        ids.DriveID
	}{
		{"zero id", ctx, "c", ids.DriveID{}},
		{"empty cluster id", ctx, "", ids.NewDriveID()},
		{"cancelled context", cancelled, "c", ids.NewDriveID()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := FormatWithID(tc.ctx, dir, tc.clusterID, tc.id); err == nil {
				t.Fatal("format succeeded")
			}
			if _, err := os.Stat(filepath.Join(dir, ".unio", "format.pb")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("format record written for rejected arguments: %v", err)
			}
		})
	}
}

func TestFormatWithIDOnFormattedDirectoryChangesNothing(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if err := FormatWithID(ctx, dir, "c", ids.NewDriveID()); err != nil {
		t.Fatalf("first format: %v", err)
	}
	before := treeSnapshot(t, dir)
	err := FormatWithID(ctx, dir, "c", ids.NewDriveID())
	if !errors.Is(err, ErrAlreadyFormatted) {
		t.Fatalf("second format: err = %v, want ErrAlreadyFormatted", err)
	}
	requireSameTree(t, dir, before)
}

func TestFormatWithIDRaceHasOneWinner(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	const n = 16
	rivals := make([]ids.DriveID, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		rivals[i] = ids.NewDriveID()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = FormatWithID(ctx, dir, "c", rivals[i])
		}()
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, err := range errs {
		switch {
		case err == nil:
			if winner != -1 {
				t.Fatalf("callers %d and %d both formatted the drive", winner, i)
			}
			winner = i
		case !errors.Is(err, ErrAlreadyFormatted):
			t.Fatalf("caller %d: err = %v, want nil or ErrAlreadyFormatted", i, err)
		}
	}
	if winner == -1 {
		t.Fatal("no caller formatted the drive")
	}
	rec, err := loadFormat(mustLayout(t, dir))
	if err != nil {
		t.Fatalf("load record: %v", err)
	}
	if rec.id != rivals[winner] {
		t.Fatalf("record names %s, want the winner's %s", rec.id, rivals[winner])
	}
}

func mustLayout(t *testing.T, dir string) layout {
	t.Helper()
	lay, err := newLayout(dir)
	if err != nil {
		t.Fatalf("layout: %v", err)
	}
	return lay
}

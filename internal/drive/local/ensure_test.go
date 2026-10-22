package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestEnsureFormattedBranches(t *testing.T) {
	ctx := context.Background()
	id := ids.NewDriveID()
	other := ids.NewDriveID()
	tests := []struct {
		name     string
		prepare  func(t *testing.T, dir string) // nil leaves the directory unformatted
		cluster  string
		want     ids.DriveID
		wantErr  error    // nil with ok==true; sentinel checked with errors.Is
		anyErr   bool     // any error at all (corrupt record)
		wantText []string // hex ids the error message must name
		created  bool     // the call formats the directory
	}{
		{
			name:    "unformatted directory is formatted",
			cluster: "c",
			want:    id,
			created: true,
		},
		{
			name:    "same cluster and id is accepted without change",
			prepare: formatWith("c", id),
			cluster: "c",
			want:    id,
		},
		{
			name:    "other cluster is refused",
			prepare: formatWith("other", id),
			cluster: "c",
			want:    id,
			wantErr: ErrClusterMismatch,
		},
		{
			name:     "other id is refused and both ids are named",
			prepare:  formatWith("c", other),
			cluster:  "c",
			want:     id,
			wantErr:  ErrDriveIDMismatch,
			wantText: []string{other.String(), id.String()},
		},
		{
			name:    "cluster is reported first when both differ",
			prepare: formatWith("other", other),
			cluster: "c",
			want:    id,
			wantErr: ErrClusterMismatch,
		},
		{
			name: "corrupt record is an error and is not overwritten",
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				if err := FormatWithID(context.Background(), dir, "c", id); err != nil {
					t.Fatalf("format: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".unio", "format.pb"), []byte{0xff, 0x00, 0x13}, 0o600); err != nil {
					t.Fatalf("corrupt: %v", err)
				}
			},
			cluster: "c",
			want:    id,
			anyErr:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.prepare != nil {
				tc.prepare(t, dir)
			}
			before := treeSnapshot(t, dir)

			err := EnsureFormatted(ctx, dir, tc.cluster, tc.want)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.anyErr:
				if err == nil || errors.Is(err, ErrNotFormatted) {
					t.Fatalf("err = %v, want a read or decode error", err)
				}
			default:
				if err != nil {
					t.Fatalf("EnsureFormatted: %v", err)
				}
			}
			for _, s := range tc.wantText {
				if err == nil || !strings.Contains(err.Error(), s) {
					t.Fatalf("error %v does not name id %s", err, s)
				}
			}

			if tc.created {
				d, err := Open(ctx, dir, Options{ClusterID: tc.cluster, ExpectedID: tc.want})
				if err != nil {
					t.Fatalf("open after format: %v", err)
				}
				if err := d.Close(); err != nil {
					t.Fatalf("close: %v", err)
				}
				return
			}
			// Every other branch leaves the directory exactly as it was.
			requireSameTree(t, dir, before)
		})
	}
}

// formatWith returns a preparation step that formats the directory with the
// given cluster and id.
func formatWith(cluster string, id ids.DriveID) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		if err := FormatWithID(context.Background(), dir, cluster, id); err != nil {
			t.Fatalf("format: %v", err)
		}
	}
}

func TestEnsureFormattedRejectsInvalidArguments(t *testing.T) {
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
			if err := EnsureFormatted(tc.ctx, dir, tc.clusterID, tc.id); err == nil {
				t.Fatal("EnsureFormatted succeeded")
			}
			if _, err := os.Stat(filepath.Join(dir, ".unio", "format.pb")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("format record written for rejected arguments: %v", err)
			}
		})
	}
}

// TestEnsureFormattedConcurrentSameIdentity runs many callers with one
// identity. Each must succeed, and the record must be intact.
func TestEnsureFormattedConcurrentSameIdentity(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	id := ids.NewDriveID()
	const n = 32
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = EnsureFormatted(ctx, dir, "c", id)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	rec, err := loadFormat(mustLayout(t, dir))
	if err != nil {
		t.Fatalf("load record: %v", err)
	}
	if rec.id != id || rec.clusterID != "c" {
		t.Fatalf("record = %s/%q, want %s/c", rec.id, rec.clusterID, id)
	}
}

// TestEnsureFormattedConcurrentRivalIDs runs callers that each bring a
// different id for one path. Exactly one id may win, the others must be
// refused with ErrDriveIDMismatch, and the record must name the winner.
func TestEnsureFormattedConcurrentRivalIDs(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	const n = 32
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
			errs[i] = EnsureFormatted(ctx, dir, "c", rivals[i])
		}()
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, err := range errs {
		switch {
		case err == nil:
			if winner != -1 {
				t.Fatalf("callers %d and %d both accepted their own id", winner, i)
			}
			winner = i
		case !errors.Is(err, ErrDriveIDMismatch):
			t.Fatalf("caller %d: err = %v, want nil or ErrDriveIDMismatch", i, err)
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

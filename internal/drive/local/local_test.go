package local_test

import (
	"context"
	"testing"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/drivetest"
	"github.com/eukalpia/unio/internal/drive/local"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// openTestDrive formats and opens a drive in a temporary directory. Segments
// default to 4 MiB so that the suite crosses segment boundaries.
func openTestDrive(t *testing.T, opts local.Options) *local.Drive {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := local.Format(ctx, dir, "test-cluster"); err != nil {
		t.Fatalf("format: %v", err)
	}
	opts.ClusterID = "test-cluster"
	if opts.SegmentSize == 0 {
		opts.SegmentSize = 4 << 20
	}
	if opts.CompactRate == 0 {
		opts.CompactRate = -1 // conformance runs without background compaction
	}
	d, err := local.Open(ctx, dir, opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return d
}

func TestLocalConformance(t *testing.T) {
	drivetest.RunSuite(t, func(t *testing.T) drive.Drive {
		return openTestDrive(t, local.Options{})
	})
}

func TestLocalConformanceBatchSync(t *testing.T) {
	drivetest.RunSuite(t, func(t *testing.T) drive.Drive {
		return openTestDrive(t, local.Options{SyncPolicy: local.SyncBatch})
	})
}

// TestLocalConformanceSharedBudget runs the suite with every drive on one
// budget of two megabytes. That is small enough that operations wait for
// memory, and large enough for one blob or packed buffer.
func TestLocalConformanceSharedBudget(t *testing.T) {
	budget := bufpool.NewBudget(2 << 20)
	drivetest.RunSuite(t, func(t *testing.T) drive.Drive {
		return openTestDrive(t, local.Options{Budget: budget})
	})
	if got := budget.InUse(); got != 0 {
		t.Fatalf("InUse after the suite = %d bytes, want 0", got)
	}
}

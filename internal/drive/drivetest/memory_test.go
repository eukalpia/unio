package drivetest_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/drivetest"
	"github.com/eukalpia/unio/internal/ids"
)

func TestMemoryConformance(t *testing.T) {
	drivetest.RunSuite(t, func(*testing.T) drive.Drive {
		return drivetest.NewMemory(ids.NewDriveID())
	})
}

func TestMemoryFaultsFailWithoutStoring(t *testing.T) {
	boom := errors.New("injected")
	var failWrites bool
	m := drivetest.NewMemory(ids.NewDriveID(), drivetest.WithFaults(drivetest.Faults{
		Write: func(ids.ShardID) error {
			if failWrites {
				return boom
			}
			return nil
		},
	}))
	defer m.Close()
	ctx := context.Background()
	id := ids.ShardID{Index: 1}

	failWrites = true
	if err := m.WriteShard(ctx, id, bytes.NewReader([]byte("abc")), 3); !errors.Is(err, boom) {
		t.Fatalf("write: err = %v, want injected error", err)
	}
	if _, err := m.StatShard(ctx, id); !errors.Is(err, drive.ErrShardNotFound) {
		t.Fatalf("failed write left a shard: %v", err)
	}

	failWrites = false
	if err := m.WriteShard(ctx, id, bytes.NewReader([]byte("abc")), 3); err != nil {
		t.Fatalf("write after fault cleared: %v", err)
	}
}

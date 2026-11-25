package offline

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

func testDrive(t *testing.T) (*Drive, ids.DriveID) {
	t.Helper()
	id := ids.NewDriveID()
	return New(id, "node-a", "10.0.0.7:9100"), id
}

// TestOfflineFailsEveryOperation checks each method of the drive.Drive
// interface, and that the failure is drive.ErrOffline rather than a shard
// error that callers could mistake for a missing shard.
func TestOfflineFailsEveryOperation(t *testing.T) {
	ctx := context.Background()
	shard := ids.ShardID{Index: 2}
	cases := []struct {
		name string
		run  func(t *testing.T, d *Drive) error
	}{
		{"WriteShard", func(_ *testing.T, d *Drive) error {
			return d.WriteShard(ctx, shard, bytes.NewReader([]byte("x")), 1)
		}},
		{"ReadShard", func(t *testing.T, d *Drive) error {
			rc, err := d.ReadShard(ctx, shard, 0, -1)
			if rc != nil {
				_ = rc.Close()
				t.Error("ReadShard returned a reader")
			}
			return err
		}},
		{"StatShard", func(_ *testing.T, d *Drive) error {
			_, err := d.StatShard(ctx, shard)
			return err
		}},
		{"DeleteShard", func(_ *testing.T, d *Drive) error {
			return d.DeleteShard(ctx, shard)
		}},
		{"WalkShards", func(t *testing.T, d *Drive) error {
			return d.WalkShards(ctx, func(drive.ShardInfo) error {
				t.Error("WalkShards called fn for an offline drive")
				return nil
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := testDrive(t)
			err := tc.run(t, d)
			if !errors.Is(err, drive.ErrOffline) {
				t.Fatalf("%s: got %v, want drive.ErrOffline", tc.name, err)
			}
			if errors.Is(err, drive.ErrShardNotFound) {
				t.Fatalf("%s: an offline drive must not report a missing shard", tc.name)
			}
		})
	}
}

// TestOfflineIdentityAndInfo checks the methods that do not fail.
func TestOfflineIdentityAndInfo(t *testing.T) {
	d, id := testDrive(t)
	if got := d.ID(); got != id {
		t.Fatalf("ID() = %s, want %s", got, id)
	}
	info, err := d.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	want := drive.Info{ID: id, Node: "node-a", Endpoint: "10.0.0.7:9100", Online: false}
	if info != want {
		t.Fatalf("Info() = %+v, want %+v", info, want)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close releases nothing, so the drive keeps failing with ErrOffline
	// and keeps its identity.
	if err := d.DeleteShard(context.Background(), ids.ShardID{}); !errors.Is(err, drive.ErrOffline) {
		t.Fatalf("DeleteShard after Close: got %v, want drive.ErrOffline", err)
	}
	if got := d.ID(); got != id {
		t.Fatalf("ID() after Close = %s, want %s", got, id)
	}
}

// TestOfflineSatisfiesDriveInterface is a compile-time check that New returns
// a value usable wherever a drive.Drive is expected.
func TestOfflineSatisfiesDriveInterface(t *testing.T) {
	var d drive.Drive = New(ids.NewDriveID(), "node-b", "")
	if _, err := d.Info(context.Background()); err != nil {
		t.Fatalf("Info through the interface: %v", err)
	}
}

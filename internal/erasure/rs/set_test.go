package rs

import (
	"testing"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/erasuretest"
	"github.com/eukalpia/unio/internal/ids"
)

func drivesOf(n int) []drive.Drive {
	ds := make([]drive.Drive, n)
	for i := range ds {
		ds[i] = erasuretest.NewDrive()
	}
	return ds
}

func TestNewValidatesArguments(t *testing.T) {
	tests := []struct {
		name   string
		drives []drive.Drive
		k, m   int
		opts   Options
	}{
		{"no data shards", drivesOf(2), 0, 2, Options{}},
		{"negative parity", drivesOf(2), 2, -1, Options{}},
		{"drive count mismatch", drivesOf(3), 2, 2, Options{}},
		{"nil drive", []drive.Drive{erasuretest.NewDrive(), nil, erasuretest.NewDrive()}, 2, 1, Options{}},
		{"negative stripe", drivesOf(3), 2, 1, Options{StripeSize: -1}},
		{"budget smaller than one stripe", drivesOf(3), 2, 1, Options{Budget: bufpool.NewBudget(1024), StripeSize: 1 << 20}},
		{"negative stall timeout", drivesOf(3), 2, 1, Options{StallTimeout: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(ids.SetID(1), tt.drives, tt.k, tt.m, tt.opts); err == nil {
				t.Fatal("New accepted invalid arguments")
			}
		})
	}
}

func TestNewReportsShape(t *testing.T) {
	s, err := New(ids.SetID(7), drivesOf(6), 4, 2, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var _ erasure.Set = s
	if s.ID() != ids.SetID(7) {
		t.Fatalf("ID = %d", s.ID())
	}
	if k, m := s.Shape(); k != 4 || m != 2 {
		t.Fatalf("Shape = %d+%d", k, m)
	}
}

// TestNewBoundsTheShardCount: shard indexes are stored as uint16 and the
// encoder works with at most 256 shards, so New enforces the bound.
func TestNewBoundsTheShardCount(t *testing.T) {
	tests := []struct {
		name    string
		k, m    int
		wantErr bool
	}{
		{"largest set", 255, 1, false},
		{"one shard over", 255, 2, true},
		{"far over", 200, 57, true},
		{"parity over", 1, 300, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(ids.SetID(1), drivesOf(tt.k+tt.m), tt.k, tt.m, Options{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("New(%d+%d): err = %v, want error: %v", tt.k, tt.m, err, tt.wantErr)
			}
		})
	}
}

package remotedrive

import (
	"math"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// TestShardIDOfBoundaries feeds the shard reference decoder the edges of its
// fields: the index is 16 bits and the DataID is exactly 16 bytes.
func TestShardIDOfBoundaries(t *testing.T) {
	data := make([]byte, 16)
	cases := []struct {
		name  string
		ref   *uniov1.ShardRef
		ok    bool
		index uint16
	}{
		{name: "index zero", ref: &uniov1.ShardRef{Data: data, Index: 0}, ok: true, index: 0},
		{name: "index at the 16-bit maximum", ref: &uniov1.ShardRef{Data: data, Index: math.MaxUint16}, ok: true, index: math.MaxUint16},
		{name: "index one past the maximum", ref: &uniov1.ShardRef{Data: data, Index: math.MaxUint16 + 1}, ok: false},
		{name: "index at the 32-bit maximum", ref: &uniov1.ShardRef{Data: data, Index: math.MaxUint32}, ok: false},
		{name: "data one byte short", ref: &uniov1.ShardRef{Data: make([]byte, 15), Index: 1}, ok: false},
		{name: "data one byte long", ref: &uniov1.ShardRef{Data: make([]byte, 17), Index: 1}, ok: false},
		{name: "no data", ref: &uniov1.ShardRef{}, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := shardIDOf(tc.ref)
			if (err == nil) != tc.ok {
				t.Fatalf("shardIDOf error = %v, want ok=%v", err, tc.ok)
			}
			if err == nil && id.Index != tc.index {
				t.Fatalf("index = %d, want %d", id.Index, tc.index)
			}
		})
	}
}

// TestDriveIDOfBoundaries checks that a drive id must be exactly 16 bytes.
func TestDriveIDOfBoundaries(t *testing.T) {
	for n, ok := range map[int]bool{0: false, 15: false, 16: true, 17: false} {
		_, err := driveIDOf(make([]byte, n))
		if (err == nil) != ok {
			t.Fatalf("drive id of %d bytes: err = %v, want ok=%v", n, err, ok)
		}
	}
}

// TestShardRefRoundTripsAtTheEdges encodes the largest shard id and decodes it
// back unchanged.
func TestShardRefRoundTripsAtTheEdges(t *testing.T) {
	id := ids.ShardID{Data: ids.NewDataID(), Index: math.MaxUint16}
	got, err := shardIDOf(shardRef(id))
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("round trip = %v, want %v", got, id)
	}
}

package logstore

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

// TestLogKeysSortByIndex checks that big-endian index encoding keeps the
// key order equal to the index order, which the range deletes rely on.
func TestLogKeysSortByIndex(t *testing.T) {
	indices := []uint64{0, 1, 255, 256, 1 << 32, math.MaxUint64 - 1, math.MaxUint64}
	for i := 1; i < len(indices); i++ {
		a, b := logKey(7, indices[i-1]), logKey(7, indices[i])
		if bytes.Compare(a, b) >= 0 {
			t.Fatalf("logKey(%d) >= logKey(%d)", indices[i-1], indices[i])
		}
	}
}

// TestGroupBoundsHoldOnlyOwnKeys checks that the key range of each group and
// tag contains that group's keys and none of its neighbours', including the
// adjacent group ids 1 and 2 and the extreme ids 0 and MaxUint32.
func TestGroupBoundsHoldOnlyOwnKeys(t *testing.T) {
	groups := []ids.PartitionID{0, 1, 2, math.MaxUint32 - 1, math.MaxUint32}
	for _, g := range groups {
		for _, tag := range []byte{tagLog, tagStable} {
			lower, upper := tagBounds(g, tag)
			if bytes.Compare(lower, upper) >= 0 {
				t.Fatalf("group %d tag %c: empty bounds", g, tag)
			}
			keys := [][]byte{
				logKey(g, 0), logKey(g, 1), logKey(g, math.MaxUint64),
				stableKey(g, nil), stableKey(g, []byte{0xff, 0xff, 0xff}),
			}
			for _, k := range keys {
				own := k[groupLen] == tag && groupOf(k) == g
				in := bytes.Compare(k, lower) >= 0 && bytes.Compare(k, upper) < 0
				if own != in {
					t.Fatalf("group %d tag %c: key %x in bounds = %v, want %v", g, tag, k, in, own)
				}
			}
		}
	}
}

// groupOf returns the group id stored in the first four bytes of key.
func groupOf(key []byte) ids.PartitionID {
	return ids.PartitionID(binary.BigEndian.Uint32(key))
}

package keys

import (
	"bytes"
	"cmp"
	"math"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

// sign normalizes an integer comparison result to -1, 0 or +1.
func sign(c int) int {
	return cmp.Compare(c, 0)
}

func TestObjectVersionOrder(t *testing.T) {
	tests := []struct {
		name   string
		lo, hi []byte
	}{
		{"newer version first", ObjectVersion(1, []byte("a"), 2), ObjectVersion(1, []byte("a"), 1)},
		{"null version last", ObjectVersion(1, []byte("a"), 1), ObjectVersion(1, []byte("a"), 0)},
		{"key before its extension", ObjectVersion(1, []byte("a"), 0), ObjectVersion(1, []byte("a\x00"), math.MaxUint64)},
		{"nul key before soh key", ObjectVersion(1, []byte{0x00}, 0), ObjectVersion(1, []byte{0x01}, 0)},
		{"smaller bucket first", ObjectVersion(1, []byte("zz"), 0), ObjectVersion(2, nil, math.MaxUint64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if bytes.Compare(tt.lo, tt.hi) >= 0 {
				t.Errorf("%x is not below %x", tt.lo, tt.hi)
			}
		})
	}
}

// FuzzOrdering checks property 2: within a bucket, key order is preserved and
// newer versions sort first; any key of a smaller bucket sorts before any key
// of a larger bucket.
func FuzzOrdering(f *testing.F) {
	f.Add(uint64(1), uint64(1), []byte("a"), []byte("a\x00"), uint64(2), uint64(1))
	f.Add(uint64(1), uint64(2), []byte{0x00}, []byte{0x01}, uint64(0), uint64(9))
	f.Add(uint64(0), uint64(math.MaxUint64), []byte{}, []byte{0xff}, uint64(math.MaxUint64), uint64(0))
	f.Fuzz(func(t *testing.T, x1, x2 uint64, k1, k2 []byte, ts1, ts2 uint64) {
		b1, b2 := ids.BucketID(x1), ids.BucketID(x2)

		gotKeys := sign(bytes.Compare(ObjectVersionPrefix(b1, k1), ObjectVersionPrefix(b1, k2)))
		if wantKeys := sign(bytes.Compare(k1, k2)); gotKeys != wantKeys {
			t.Fatalf("prefix order of %x and %x is %d, want %d", k1, k2, gotKeys, wantKeys)
		}

		gotVersions := sign(bytes.Compare(ObjectVersion(b1, k1, ts1), ObjectVersion(b1, k1, ts2)))
		if wantVersions := -cmp.Compare(ts1, ts2); gotVersions != wantVersions {
			t.Fatalf("version order of ts %d and %d is %d, want %d", ts1, ts2, gotVersions, wantVersions)
		}

		if b1 != b2 {
			gotBuckets := sign(bytes.Compare(ObjectVersion(b1, k1, ts1), ObjectVersion(b2, k2, ts2)))
			if wantBuckets := cmp.Compare(b1, b2); gotBuckets != wantBuckets {
				t.Fatalf("bucket order of %d and %d is %d, want %d", b1, b2, gotBuckets, wantBuckets)
			}
		}
	})
}

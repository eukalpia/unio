package keys

import (
	"bytes"
	"math"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

// inRange reports whether the encoded object version k lies in the key range
// of prefix in bucket b.
func inRange(b ids.BucketID, prefix []byte, k []byte) bool {
	return bytes.Compare(k, ObjectKeyStart(b, prefix)) >= 0 &&
		bytes.Compare(k, ObjectKeySeekPast(b, prefix)) < 0
}

func TestPrefixRange(t *testing.T) {
	tests := []struct {
		name      string
		bucket    ids.BucketID
		keyBucket ids.BucketID
		prefix    string
		key       string
		want      bool
	}{
		{"prefix matches", 1, 1, "61", "61 62", true},
		{"exact key matches", 1, 1, "61", "61", true},
		{"shorter key excluded", 1, 1, "61", "", false},
		{"sibling excluded", 1, 1, "61", "62", false},
		{"nul prefix matches escaped nul", 1, 1, "00", "00 01", true},
		{"nul prefix excludes soh", 1, 1, "00", "01", false},
		{"empty prefix matches all", 1, 1, "", "7a 7a", true},
		{"all ff prefix matches", 1, 1, "ff", "ff 00", true},
		{"all ff prefix excludes lower", 1, 1, "ff", "fe ff", false},
		{"other bucket excluded", 1, 2, "", "", false},
		{"next bucket ff key excluded", 255, 256, "ff", "ff", false},
		{"max bucket prefix matches", math.MaxUint64, math.MaxUint64, "ff", "ff", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := ObjectVersion(tt.keyBucket, mustHex(tt.key), 1)
			if got := inRange(tt.bucket, mustHex(tt.prefix), k); got != tt.want {
				t.Errorf("in range = %t, want %t", got, tt.want)
			}
		})
	}
}

// FuzzPrefixRange checks property 3: an object version lies in the key range
// of a prefix exactly when its key has that prefix and lives in the same
// bucket, for every timestamp.
func FuzzPrefixRange(f *testing.F) {
	f.Add(uint64(1), uint64(1), []byte("a"), []byte("ab"), uint64(3))
	f.Add(uint64(1), uint64(1), []byte{0xff}, []byte{0xff, 0x00}, uint64(0))
	f.Add(uint64(math.MaxUint64), uint64(math.MaxUint64), []byte{}, []byte{0x00}, uint64(1))
	f.Add(uint64(255), uint64(256), []byte{0xff}, []byte{0xff}, uint64(1))
	f.Fuzz(func(t *testing.T, bucket, keyBucket uint64, prefix, key []byte, ts uint64) {
		b, kb := ids.BucketID(bucket), ids.BucketID(keyBucket)
		start := ObjectKeyStart(b, prefix)
		end := ObjectKeySeekPast(b, prefix)
		if bytes.Compare(start, end) >= 0 {
			t.Fatalf("empty range [%x, %x) for prefix %x", start, end, prefix)
		}
		want := kb == b && bytes.HasPrefix(key, prefix)
		for _, v := range []uint64{ts, 0, math.MaxUint64} {
			if got := inRange(b, prefix, ObjectVersion(kb, key, v)); got != want {
				t.Fatalf("prefix %x, key %x in bucket %d (bucket %d), ts %d: in range %t, want %t",
					prefix, key, kb, b, v, got, want)
			}
		}
	})
}

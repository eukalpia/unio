package keys

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestObjectVersion(t *testing.T) {
	tests := []struct {
		name   string
		bucket ids.BucketID
		key    []byte
		ts     uint64
		want   string
	}{
		{
			name:   "worked example",
			bucket: 1,
			key:    []byte("a"),
			ts:     5,
			want:   "4f " + bucket1 + " 61 00 01 ff ff ff ff ff ff ff fa",
		},
		{
			name:   "null version sorts last",
			bucket: 1,
			key:    []byte("a"),
			ts:     0,
			want:   "4f " + bucket1 + " 61 00 01 ff ff ff ff ff ff ff ff",
		},
		{
			name:   "empty key",
			bucket: 0x0102030405060708,
			key:    nil,
			ts:     1,
			want:   "4f 01 02 03 04 05 06 07 08 00 01 ff ff ff ff ff ff ff fe",
		},
		{
			name:   "only nul bytes",
			bucket: 2,
			key:    []byte{0x00, 0x00},
			ts:     7,
			want:   "4f " + bucket2 + " 00 ff 00 ff 00 01 ff ff ff ff ff ff ff f8",
		},
		{
			name:   "only soh bytes",
			bucket: 3,
			key:    []byte{0x01, 0x01},
			ts:     7,
			want:   "4f " + bucket3 + " 01 01 00 01 ff ff ff ff ff ff ff f8",
		},
		{
			name:   "max bucket and timestamp",
			bucket: math.MaxUint64,
			key:    nil,
			ts:     math.MaxUint64,
			want:   "4f ff ff ff ff ff ff ff ff 00 01 00 00 00 00 00 00 00 00",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ObjectVersion(tt.bucket, tt.key, tt.ts)
			if want := mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("ObjectVersion = %x, want %x", got, want)
			}
		})
	}
}

func TestObjectVersionPrefix(t *testing.T) {
	tests := []struct {
		name   string
		bucket ids.BucketID
		key    string
		want   string
	}{
		{"plain key", 1, "61", "4f " + bucket1 + " 61 00 01"},
		{"empty key", 1, "", "4f " + bucket1 + " 00 01"},
		{"nul key", 1, "00", "4f " + bucket1 + " 00 ff 00 01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ObjectVersionPrefix(tt.bucket, mustHex(tt.key))
			if want := mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("ObjectVersionPrefix = %x, want %x", got, want)
			}
			version := ObjectVersion(tt.bucket, mustHex(tt.key), 9)
			if !bytes.HasPrefix(version, got) {
				t.Errorf("ObjectVersion %x does not start with its prefix %x", version, got)
			}
		})
	}
}

func TestObjectBucketPrefix(t *testing.T) {
	tests := []struct {
		name   string
		bucket ids.BucketID
		want   string
	}{
		{"bucket 1", 1, "4f " + bucket1},
		{"max bucket", math.MaxUint64, "4f ff ff ff ff ff ff ff ff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := ObjectBucketPrefix(tt.bucket), mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("ObjectBucketPrefix = %x, want %x", got, want)
			}
		})
	}
}

func TestObjectKeyStart(t *testing.T) {
	tests := []struct {
		name   string
		bucket ids.BucketID
		prefix string
		want   string
	}{
		{"name prefix has no terminator", 1, "61", "4f " + bucket1 + " 61"},
		{"empty prefix is bucket start", 1, "", "4f " + bucket1},
		{"nul prefix is escaped", 1, "00", "4f " + bucket1 + " 00 ff"},
		{"nul soh prefix", 1, "00 01", "4f " + bucket1 + " 00 ff 01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := ObjectKeyStart(tt.bucket, mustHex(tt.prefix)), mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("ObjectKeyStart = %x, want %x", got, want)
			}
		})
	}
}

func TestObjectKeySeekPast(t *testing.T) {
	tests := []struct {
		name   string
		bucket ids.BucketID
		prefix string
		want   string
	}{
		{"successor of last name byte", 1, "61", "4f " + bucket1 + " 62"},
		{"empty prefix ends the bucket", 1, "", "4f " + bucket2},
		{"nul prefix", 1, "00", "4f " + bucket1 + " 01"},
		{"all ff prefix ends the bucket", 1, "ff", "4f " + bucket2},
		{"two ff prefix ends the bucket", 1, "ff ff", "4f " + bucket2},
		{"ff then nul", 1, "ff 00", "4f " + bucket1 + " ff 01"},
		{"max bucket, empty prefix", math.MaxUint64, "", "50"},
		{"bucket 255, empty prefix", 255, "", "4f 00 00 00 00 00 00 01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ObjectKeySeekPast(tt.bucket, mustHex(tt.prefix))
			if got == nil {
				t.Fatal("ObjectKeySeekPast returned nil")
			}
			if want := mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("ObjectKeySeekPast = %x, want %x", got, want)
			}
		})
	}
}

func TestParseObjectVersion(t *testing.T) {
	valid := []struct {
		name   string
		in     string
		bucket ids.BucketID
		key    string
		ts     uint64
	}{
		{"worked example", "4f " + bucket1 + " 61 00 01 ff ff ff ff ff ff ff fa", 1, "61", 5},
		{"null version", "4f " + bucket1 + " 61 00 01 ff ff ff ff ff ff ff ff", 1, "61", 0},
		{"empty key", "4f " + bucket1 + " 00 01 ff ff ff ff ff ff ff ff", 1, "", 0},
		{"only nul bytes", "4f " + bucket1 + " 00 ff 00 ff 00 01 ff ff ff ff ff ff ff f8", 1, "00 00", 7},
		{"only soh bytes", "4f " + bucket1 + " 01 00 01 ff ff ff ff ff ff ff f8", 1, "01", 7},
		{"nul then soh", "4f " + bucket1 + " 00 ff 01 00 01 ff ff ff ff ff ff ff f8", 1, "00 01", 7},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			b, key, ts, err := ParseObjectVersion(mustHex(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b != tt.bucket || !bytes.Equal(key, mustHex(tt.key)) || ts != tt.ts {
				t.Errorf("got (%d, %x, %d), want (%d, %s, %d)", b, key, ts, tt.bucket, tt.key, tt.ts)
			}
		})
	}

	malformed := []struct {
		name, in string
	}{
		{"empty input", ""},
		{"wrong leading byte", "50 " + bucket1 + " 61 00 01 ff ff ff ff ff ff ff fa"},
		{"no bucket id", "4f"},
		{"short bucket id", "4f 00 00 00"},
		{"unterminated key", "4f " + bucket1 + " 61 62"},
		{"dangling nul", "4f " + bucket1 + " 61 00"},
		{"invalid escape", "4f " + bucket1 + " 00 02 00 01 ff ff ff ff ff ff ff fa"},
		{"terminator without timestamp", "4f " + bucket1 + " 61 00 01"},
		{"short timestamp", "4f " + bucket1 + " 61 00 01 ff ff ff ff ff ff ff"},
		{"long timestamp", "4f " + bucket1 + " 61 00 01 ff ff ff ff ff ff ff ff 00"},
	}
	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := ParseObjectVersion(mustHex(tt.in))
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}

// FuzzObjectVersionRoundTrip checks that ParseObjectVersion inverts
// ObjectVersion for arbitrary bucket, key and timestamp.
func FuzzObjectVersionRoundTrip(f *testing.F) {
	f.Add(uint64(1), []byte("a"), uint64(5))
	f.Add(uint64(1), []byte{0x00, 0x01}, uint64(0))
	f.Add(uint64(math.MaxUint64), []byte{}, uint64(math.MaxUint64))
	f.Fuzz(func(t *testing.T, bucket uint64, key []byte, ts uint64) {
		b := ids.BucketID(bucket)
		gb, gk, gts, err := ParseObjectVersion(ObjectVersion(b, key, ts))
		if err != nil {
			t.Fatalf("ParseObjectVersion: %v", err)
		}
		if gb != b || !bytes.Equal(gk, key) || gts != ts {
			t.Fatalf("round trip got (%d, %x, %d), want (%d, %x, %d)", gb, gk, gts, b, key, ts)
		}
	})
}

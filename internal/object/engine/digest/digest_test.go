package digest

import (
	"encoding/hex"
	"testing"

	"github.com/eukalpia/unio/internal/object"
)

// Check values for the standard check input "123456789".
func TestNewCheckValues(t *testing.T) {
	tests := []struct {
		name string
		alg  object.ChecksumAlgorithm
		want string
	}{
		{"crc32", object.ChecksumCRC32, "cbf43926"},
		{"crc32c", object.ChecksumCRC32C, "e3069283"},
		{"crc64nvme", object.ChecksumCRC64NVME, "ae8b14860a799888"},
		{"sha1", object.ChecksumSHA1, "f7c3bc1d808e04732adf679965ccc34ca7ae3441"},
		{"sha256", object.ChecksumSHA256, "15e2b0d3c33891ebb0f1ef609ec419420c20e320ce94c65fbc8c3312448eb225"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := New(tt.alg)
			if err != nil {
				t.Fatal(err)
			}
			h.Write([]byte("123456789"))
			if got := hex.EncodeToString(h.Sum(nil)); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNewRejectsNoneAndUnknown(t *testing.T) {
	for _, alg := range []object.ChecksumAlgorithm{object.ChecksumNone, object.ChecksumAlgorithm(200)} {
		if _, err := New(alg); err == nil {
			t.Fatalf("algorithm %d: want error", alg)
		}
	}
}

package fakes

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestChecksumKnownValues(t *testing.T) {
	// "123456789" is the standard check input for every algorithm.
	data := []byte("123456789")
	sha1Sum := sha1.Sum(data)
	sha256Sum := sha256.Sum256(data)
	tests := []struct {
		name string
		alg  object.ChecksumAlgorithm
		want string // hex digest
	}{
		{name: "CRC32", alg: object.ChecksumCRC32, want: "cbf43926"},
		{name: "CRC32C", alg: object.ChecksumCRC32C, want: "e3069283"},
		{name: "CRC64NVME", alg: object.ChecksumCRC64NVME, want: "ae8b14860a799888"},
		{name: "SHA1", alg: object.ChecksumSHA1, want: hex.EncodeToString(sha1Sum[:])},
		{name: "SHA256", alg: object.ChecksumSHA256, want: hex.EncodeToString(sha256Sum[:])},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checksumOf(tt.alg, data)
			wantCode(t, err, "")
			if got.Algorithm != tt.alg || got.Type != object.ChecksumFullObject || got.Parts != 0 {
				t.Errorf("checksum = %+v, want a full-object %v checksum", got, tt.alg)
			}
			if hex.EncodeToString(got.Value) != tt.want {
				t.Errorf("digest = %x, want %s", got.Value, tt.want)
			}
		})
	}
}

func TestChecksumNoneAndUnknown(t *testing.T) {
	zero, err := checksumOf(object.ChecksumNone, []byte("data"))
	wantCode(t, err, "")
	if !zero.IsZero() || zero.Value != nil {
		t.Errorf("ChecksumNone = %+v, want the zero checksum", zero)
	}

	_, err = checksumOf(object.ChecksumAlgorithm(200), []byte("data"))
	wantCode(t, err, s3err.InvalidArgument)
	wantCode(t, checkAlgorithm(object.ChecksumAlgorithm(200)), s3err.InvalidArgument)
	wantCode(t, checkAlgorithm(object.ChecksumCRC32C), "")
}

func TestCompositeChecksum(t *testing.T) {
	parts := [][]byte{[]byte("alpha"), []byte("beta"), []byte("gamma")}
	tests := []struct {
		name   string
		alg    object.ChecksumAlgorithm
		digest func(p []byte) []byte
	}{
		{
			name: "CRC32",
			alg:  object.ChecksumCRC32,
			digest: func(p []byte) []byte {
				return binary.BigEndian.AppendUint32(nil, crc32.ChecksumIEEE(p))
			},
		},
		{
			name: "SHA256",
			alg:  object.ChecksumSHA256,
			digest: func(p []byte) []byte {
				sum := sha256.Sum256(p)
				return sum[:]
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var concat []byte
			for _, p := range parts {
				concat = append(concat, tt.digest(p)...)
			}
			want := tt.digest(concat)

			got, err := compositeChecksum(tt.alg, parts)
			wantCode(t, err, "")
			if got.Type != object.ChecksumComposite || got.Parts != len(parts) || got.Algorithm != tt.alg {
				t.Errorf("composite = %+v, want composite %v over %d parts", got, tt.alg, len(parts))
			}
			if !bytes.Equal(got.Value, want) {
				t.Errorf("composite digest = %x, want %x", got.Value, want)
			}
		})
	}
}

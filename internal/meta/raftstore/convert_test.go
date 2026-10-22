package raftstore

import (
	"math"
	"strconv"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// TestUint32LimitRejectsOutOfRange checks the bounds check that runs before a
// page size is converted to its 4-byte query field.
func TestUint32LimitRejectsOutOfRange(t *testing.T) {
	for _, n := range []int{-1, math.MinInt32, -1 << 40} {
		if _, err := uint32Limit(n); err == nil {
			t.Fatalf("uint32Limit(%d) accepted a negative size", n)
		}
	}
	for _, n := range []int{0, 1, object.MaxListKeys + 1} {
		got, err := uint32Limit(n)
		if err != nil || int(got) != n {
			t.Fatalf("uint32Limit(%d) = %d, %v", n, got, err)
		}
	}
	if strconv.IntSize == 64 {
		if _, err := uint32Limit(1 << 32); err == nil {
			t.Fatal("uint32Limit accepted 2^32")
		}
	}
}

// TestChecksumAlgorithmOfRejectsUnknownValues checks that a stored checksum
// algorithm outside the defined range is an error, not a truncated value.
func TestChecksumAlgorithmOfRejectsUnknownValues(t *testing.T) {
	for _, v := range []pb.ChecksumAlgorithm{-1, 6, 256, 1 << 20} {
		if _, err := checksumAlgorithmOf(v); err == nil {
			t.Fatalf("checksumAlgorithmOf(%d) accepted an unknown algorithm", v)
		}
	}
	want := map[pb.ChecksumAlgorithm]object.ChecksumAlgorithm{
		pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_NONE:      object.ChecksumNone,
		pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32:     object.ChecksumCRC32,
		pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C:    object.ChecksumCRC32C,
		pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC64NVME: object.ChecksumCRC64NVME,
		pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA1:      object.ChecksumSHA1,
		pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256:    object.ChecksumSHA256,
	}
	for in, out := range want {
		got, err := checksumAlgorithmOf(in)
		if err != nil || got != out {
			t.Fatalf("checksumAlgorithmOf(%d) = %v, %v; want %v", in, got, err, out)
		}
	}
}

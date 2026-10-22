package recordpb

import (
	"math"
	"strconv"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// allChecksumAlgorithms lists every object.ChecksumAlgorithm value.
func allChecksumAlgorithms() []object.ChecksumAlgorithm {
	return []object.ChecksumAlgorithm{
		object.ChecksumNone, object.ChecksumCRC32, object.ChecksumCRC32C,
		object.ChecksumCRC64NVME, object.ChecksumSHA1, object.ChecksumSHA256,
	}
}

// int64Only skips a test that needs an int wider than 32 bits.
func int64Only(t *testing.T) {
	t.Helper()
	if strconv.IntSize < 64 {
		t.Skip("needs a 64-bit int")
	}
}

// above32 returns 1<<32 as an int. It is not a constant so that the package
// still compiles on 32-bit platforms, where the value wraps.
func above32() int {
	n := uint64(1) << 32
	return int(n)
}

// maxUint32Int returns math.MaxUint32 as an int, with the same caveat as
// above32.
func maxUint32Int() int {
	n := uint64(math.MaxUint32)
	return int(n)
}

func TestChecksumRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   object.Checksum
	}{
		{"zero value", object.Checksum{}},
		{"crc32 full object", object.Checksum{Algorithm: object.ChecksumCRC32, Type: object.ChecksumFullObject, Value: []byte{0xde, 0xad, 0xbe, 0xef}}},
		{"sha256 composite", object.Checksum{Algorithm: object.ChecksumSHA256, Type: object.ChecksumComposite, Value: make([]byte, 32), Parts: 12}},
		{"empty value is equivalent to nil", object.Checksum{Algorithm: object.ChecksumCRC32C, Value: []byte{}}},
	}
	for _, a := range allChecksumAlgorithms() {
		tests = append(tests, struct {
			name string
			in   object.Checksum
		}{a.String() + " composite", object.Checksum{Algorithm: a, Type: object.ChecksumComposite, Value: []byte{1}, Parts: 2}})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ChecksumToProto(tt.in)
			if err != nil {
				t.Fatalf("ChecksumToProto: %v", err)
			}
			got, err := ChecksumFromProto(p)
			if err != nil {
				t.Fatalf("ChecksumFromProto: %v", err)
			}
			if diff := cmp.Diff(tt.in, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestChecksumEnumMapping(t *testing.T) {
	tests := []struct {
		alg  object.ChecksumAlgorithm
		want uniov1.ChecksumAlgorithm
	}{
		{object.ChecksumNone, uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_NONE},
		{object.ChecksumCRC32, uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32},
		{object.ChecksumCRC32C, uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C},
		{object.ChecksumCRC64NVME, uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC64NVME},
		{object.ChecksumSHA1, uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA1},
		{object.ChecksumSHA256, uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256},
	}
	for _, tt := range tests {
		p, err := ChecksumToProto(object.Checksum{Algorithm: tt.alg})
		if err != nil {
			t.Fatalf("ChecksumToProto(%v): %v", tt.alg, err)
		}
		if p.GetAlgorithm() != tt.want {
			t.Errorf("algorithm %v encoded as %v, want %v", tt.alg, p.GetAlgorithm(), tt.want)
		}
	}
}

func TestChecksumToProtoErrors(t *testing.T) {
	tests := []struct {
		name string
		in   object.Checksum
	}{
		{"unknown algorithm", object.Checksum{Algorithm: object.ChecksumAlgorithm(6)}},
		{"unknown checksum type", object.Checksum{Type: object.ChecksumType(2)}},
		{"negative parts", object.Checksum{Parts: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ChecksumToProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

// TestChecksumPartsUint32Boundary checks both edges of the uint32 range for
// Parts. Values above the range are rejected instead of truncated.
func TestChecksumPartsUint32Boundary(t *testing.T) {
	int64Only(t)
	p, err := ChecksumToProto(object.Checksum{Parts: maxUint32Int()})
	if err != nil {
		t.Fatalf("ChecksumToProto(max uint32): %v", err)
	}
	if p.GetParts() != math.MaxUint32 {
		t.Errorf("Parts = %d, want %d", p.GetParts(), uint32(math.MaxUint32))
	}
	if _, err := ChecksumToProto(object.Checksum{Parts: above32()}); err == nil {
		t.Error("want error for parts above uint32, got nil")
	}
}

func TestChecksumFromProtoErrors(t *testing.T) {
	tests := []struct {
		name string
		in   *uniov1.Checksum
	}{
		{"nil message", nil},
		{"unknown algorithm", &uniov1.Checksum{Algorithm: 6}},
		{"negative algorithm", &uniov1.Checksum{Algorithm: -1}},
		{"unknown checksum type", &uniov1.Checksum{Type: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ChecksumFromProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

// TestChecksumValueIsCopied checks that the converters do not alias the
// caller's byte slice.
func TestChecksumValueIsCopied(t *testing.T) {
	in := object.Checksum{Algorithm: object.ChecksumCRC32, Value: []byte{1, 2, 3, 4}}
	p, err := ChecksumToProto(in)
	if err != nil {
		t.Fatalf("ChecksumToProto: %v", err)
	}
	in.Value[0] = 0xff
	if p.GetValue()[0] != 1 {
		t.Error("ChecksumToProto aliases the source Value")
	}
}

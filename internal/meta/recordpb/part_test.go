package recordpb

import (
	"math"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/google/go-cmp/cmp"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

func TestPartRoundTrip(t *testing.T) {
	for _, np := range partFixtures() {
		t.Run(np.name, func(t *testing.T) {
			p, err := PartToProto(np.rec)
			if err != nil {
				t.Fatalf("PartToProto: %v", err)
			}
			got, err := PartFromProto(p)
			if err != nil {
				t.Fatalf("PartFromProto: %v", err)
			}
			if diff := cmp.Diff(np.rec, got, equivOpts()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestPartDataIsAlways16Bytes checks that a DataID is encoded as 16 bytes,
// including the zero DataID.
func TestPartDataIsAlways16Bytes(t *testing.T) {
	for _, rec := range []meta.PartRecord{{}, {Data: fixtureDataID(5)}} {
		p, err := PartToProto(rec)
		if err != nil {
			t.Fatalf("PartToProto: %v", err)
		}
		if len(p.GetData()) != 16 {
			t.Errorf("Data encoded as %d bytes, want 16", len(p.GetData()))
		}
	}
}

func TestPartToProtoErrors(t *testing.T) {
	negNumber := fixturePart(1)
	negNumber.Number = -1
	negSize := fixturePart(1)
	negSize.Size = -1
	badChecksum := fixturePart(1)
	badChecksum.Checksum = object.Checksum{Algorithm: object.ChecksumAlgorithm(9)}
	badLayout := fixturePart(1)
	badLayout.Layout = erasure.Layout{DataShards: -1}

	tests := []struct {
		name string
		in   meta.PartRecord
	}{
		{"negative number", negNumber},
		{"negative size", negSize},
		{"invalid checksum", badChecksum},
		{"invalid layout", badLayout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PartToProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestPartToProtoNumberOverflow(t *testing.T) {
	int64Only(t)
	p := fixturePart(1)
	p.Number = above32()
	if _, err := PartToProto(p); err == nil {
		t.Error("want error for number above uint32, got nil")
	}
	p.Number = maxUint32Int()
	if _, err := PartToProto(p); err != nil {
		t.Errorf("number at uint32 max: %v", err)
	}
}

func TestPartFromProtoErrors(t *testing.T) {
	valid := func() *uniov1.PartRecord {
		return &uniov1.PartRecord{Number: 1, Data: make([]byte, 16)}
	}
	withData := func(n int) *uniov1.PartRecord {
		p := valid()
		p.Data = make([]byte, n)
		return p
	}
	negSize := valid()
	negSize.Size = -1
	badAlg := valid()
	badAlg.Checksum = &uniov1.Checksum{Algorithm: 6}
	badLayout := valid()
	badLayout.Layout = &uniov1.ErasureLayout{Drives: [][]byte{make([]byte, 3)}}

	tests := []struct {
		name string
		in   *uniov1.PartRecord
	}{
		{"nil message", nil},
		{"missing data id", &uniov1.PartRecord{Number: 1}},
		{"data id of 15 bytes", withData(15)},
		{"data id of 17 bytes", withData(17)},
		{"negative size", negSize},
		{"unknown checksum algorithm", badAlg},
		{"malformed drive id in layout", badLayout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PartFromProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

// TestPartFromProtoAbsentNested checks that absent nested messages decode
// as the zero value of their domain type.
func TestPartFromProtoAbsentNested(t *testing.T) {
	got, err := PartFromProto(&uniov1.PartRecord{Number: 2, Data: make([]byte, 16)})
	if err != nil {
		t.Fatalf("PartFromProto: %v", err)
	}
	if !got.Checksum.IsZero() || got.Checksum.Value != nil {
		t.Errorf("absent checksum decoded as %+v, want zero", got.Checksum)
	}
	if diff := cmp.Diff(erasure.Layout{}, got.Layout, equivOpts()); diff != "" {
		t.Errorf("absent layout decoded wrongly (-want +got):\n%s", diff)
	}
}

// TestPartNumberAndSizeRange checks that boundary values in range are
// accepted: a zero number and a zero size are valid.
func TestPartNumberAndSizeRange(t *testing.T) {
	for _, p := range []meta.PartRecord{{Number: 0, Size: 0}, {Number: 1, Size: math.MaxInt64}} {
		if _, err := PartToProto(p); err != nil {
			t.Errorf("PartToProto(%+v): %v", p, err)
		}
	}
}

package recordpb

import (
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/google/go-cmp/cmp"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

func TestObjectRoundTrip(t *testing.T) {
	for _, nc := range objectFixtures() {
		t.Run(nc.name, func(t *testing.T) {
			p, err := ObjectToProto(nc.rec)
			if err != nil {
				t.Fatalf("ObjectToProto: %v", err)
			}
			got, err := ObjectFromProto(p)
			if err != nil {
				t.Fatalf("ObjectFromProto: %v", err)
			}
			if diff := cmp.Diff(nc.rec, got, equivOpts()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestObjectInlinePresenceInMemory checks that Inline presence survives the
// in-memory conversion in both directions.
func TestObjectInlinePresenceInMemory(t *testing.T) {
	tests := []struct {
		name   string
		inline []byte
	}{
		{"nil is absent", nil},
		{"empty is present", []byte{}},
		{"data is present", []byte("payload")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := withInline(fixtureFullObject(), tt.inline)
			p, err := ObjectToProto(rec)
			if err != nil {
				t.Fatalf("ObjectToProto: %v", err)
			}
			if (p.Inline == nil) != (tt.inline == nil) {
				t.Errorf("proto Inline nil = %v, want %v", p.Inline == nil, tt.inline == nil)
			}
			got, err := ObjectFromProto(p)
			if err != nil {
				t.Fatalf("ObjectFromProto: %v", err)
			}
			if (got.Inline == nil) != (tt.inline == nil) {
				t.Errorf("domain Inline nil = %v, want %v", got.Inline == nil, tt.inline == nil)
			}
		})
	}
}

// TestObjectCopiesBytes checks that the converters do not alias the
// caller's Inline bytes.
func TestObjectCopiesBytes(t *testing.T) {
	rec := withInline(fixtureFullObject(), []byte{1, 2, 3})
	p, err := ObjectToProto(rec)
	if err != nil {
		t.Fatalf("ObjectToProto: %v", err)
	}
	rec.Inline[0] = 0xff
	if p.Inline[0] != 1 {
		t.Error("ObjectToProto aliases the source Inline")
	}
}

func TestObjectToProtoErrors(t *testing.T) {
	negSize := fixtureFullObject()
	negSize.Size = -1

	badChecksum := fixtureFullObject()
	badChecksum.Checksum = object.Checksum{Algorithm: object.ChecksumAlgorithm(9)}

	badPart := fixtureFullObject()
	badPart.Parts = []meta.PartRecord{fixturePart(1)}
	badPart.Parts[0].Size = -5

	badModTime := fixtureFullObject()
	badModTime.ModTime = time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)

	badPartModTime := fixtureFullObject()
	badPartModTime.Parts = []meta.PartRecord{fixturePart(1)}
	badPartModTime.Parts[0].ModTime = time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		in   meta.ObjectRecord
	}{
		{"negative size", negSize},
		{"invalid checksum", badChecksum},
		{"invalid part", badPart},
		{"modification time after the nanosecond range", badModTime},
		{"part modification time before the nanosecond range", badPartModTime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ObjectToProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestObjectFromProtoErrors(t *testing.T) {
	negSize := &uniov1.ObjectRecord{Bucket: "b", Size: -1}
	nilPart := &uniov1.ObjectRecord{Bucket: "b", Parts: []*uniov1.PartRecord{nil}}
	badPart := &uniov1.ObjectRecord{Bucket: "b", Parts: []*uniov1.PartRecord{{Number: 1, Data: make([]byte, 3)}}}

	tests := []struct {
		name string
		in   *uniov1.ObjectRecord
	}{
		{"nil message", nil},
		{"negative size", negSize},
		{"nil entry in parts", nilPart},
		{"malformed part", badPart},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ObjectFromProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

// TestObjectFromProtoAbsentNested checks that absent Attributes and Checksum
// decode as zero values rather than errors.
func TestObjectFromProtoAbsentNested(t *testing.T) {
	got, err := ObjectFromProto(&uniov1.ObjectRecord{Bucket: "b", Key: "k"})
	if err != nil {
		t.Fatalf("ObjectFromProto: %v", err)
	}
	want := meta.ObjectRecord{Bucket: "b", Key: "k"}
	if diff := cmp.Diff(want, got, equivOpts()); diff != "" {
		t.Errorf("absent nested messages (-want +got):\n%s", diff)
	}
}

// TestObjectEmptyMetadataDecodesNil checks the nil/empty equivalence rule:
// an empty user-metadata map on the wire decodes to nil.
func TestObjectEmptyMetadataDecodesNil(t *testing.T) {
	p := &uniov1.ObjectRecord{
		Bucket:     "b",
		Attributes: &uniov1.ObjectAttributes{UserMetadata: map[string]string{}},
	}
	got, err := ObjectFromProto(p)
	if err != nil {
		t.Fatalf("ObjectFromProto: %v", err)
	}
	if got.UserMetadata != nil {
		t.Errorf("UserMetadata = %v, want nil", got.UserMetadata)
	}
}

// TestObjectPartsKeepOrder checks that parts keep their order.
func TestObjectPartsKeepOrder(t *testing.T) {
	rec := fixtureFullObject()
	rec.Multipart = true
	rec.Parts = []meta.PartRecord{fixturePart(3), fixturePart(1), fixturePart(2)}
	p, err := ObjectToProto(rec)
	if err != nil {
		t.Fatalf("ObjectToProto: %v", err)
	}
	got, err := ObjectFromProto(p)
	if err != nil {
		t.Fatalf("ObjectFromProto: %v", err)
	}
	for i, want := range []int{3, 1, 2} {
		if got.Parts[i].Number != want {
			t.Errorf("Parts[%d].Number = %d, want %d", i, got.Parts[i].Number, want)
		}
	}
}

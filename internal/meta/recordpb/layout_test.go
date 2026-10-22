package recordpb

import (
	"math"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

func TestLayoutRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   erasure.Layout
	}{
		{"zero value", erasure.Layout{}},
		{"full layout with one unwritten shard", fixtureLayout()},
		{"all shards unwritten", erasure.Layout{
			Set: 1, DataShards: 2, ParityShards: 2, StripeSize: 4096,
			Drives: []ids.DriveID{{}, {}, {}, {}},
		}},
		{"no drives recorded", erasure.Layout{Set: 3, DataShards: 1, ParityShards: 0, StripeSize: 1}},
		{"empty drives equal nil", erasure.Layout{Set: 3, Drives: []ids.DriveID{}}},
		{"max uint32 set", erasure.Layout{Set: math.MaxUint32, DataShards: 1, ParityShards: 1, StripeSize: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := LayoutToProto(tt.in)
			if err != nil {
				t.Fatalf("LayoutToProto: %v", err)
			}
			got, err := LayoutFromProto(p)
			if err != nil {
				t.Fatalf("LayoutFromProto: %v", err)
			}
			if diff := cmp.Diff(tt.in, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLayoutDriveWireForm checks the encoding of drive entries: a zero
// DriveID is a zero-length entry, and a non-zero DriveID is 16 bytes.
func TestLayoutDriveWireForm(t *testing.T) {
	p, err := LayoutToProto(erasure.Layout{Drives: []ids.DriveID{{}, fixtureDriveID(9)}})
	if err != nil {
		t.Fatalf("LayoutToProto: %v", err)
	}
	drives := p.GetDrives()
	if len(drives) != 2 {
		t.Fatalf("got %d drive entries, want 2", len(drives))
	}
	if len(drives[0]) != 0 {
		t.Errorf("zero DriveID encoded as %d bytes, want 0", len(drives[0]))
	}
	if len(drives[1]) != 16 {
		t.Errorf("non-zero DriveID encoded as %d bytes, want 16", len(drives[1]))
	}
}

func TestLayoutToProtoErrors(t *testing.T) {
	tests := []struct {
		name string
		in   erasure.Layout
	}{
		{"negative data shards", erasure.Layout{DataShards: -1}},
		{"negative parity shards", erasure.Layout{ParityShards: -1}},
		{"negative stripe size", erasure.Layout{StripeSize: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LayoutToProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestLayoutToProtoUint32Overflow(t *testing.T) {
	int64Only(t)
	tests := []struct {
		name string
		in   erasure.Layout
	}{
		{"data shards above uint32", erasure.Layout{DataShards: above32()}},
		{"parity shards above uint32", erasure.Layout{ParityShards: above32()}},
		{"stripe size above uint32", erasure.Layout{StripeSize: above32()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LayoutToProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestLayoutFromProtoErrors(t *testing.T) {
	tests := []struct {
		name string
		in   *uniov1.ErasureLayout
	}{
		{"nil message", nil},
		{"drive id of 1 byte", &uniov1.ErasureLayout{Drives: [][]byte{{0x01}}}},
		{"drive id of 15 bytes", &uniov1.ErasureLayout{Drives: [][]byte{make([]byte, 15)}}},
		{"drive id of 17 bytes", &uniov1.ErasureLayout{Drives: [][]byte{make([]byte, 17)}}},
		{"malformed entry after valid ones", &uniov1.ErasureLayout{Drives: [][]byte{make([]byte, 16), make([]byte, 3)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LayoutFromProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

// TestLayoutFromProtoDriveIDs checks that 16 bytes decode to the DriveID
// and a zero-length entry decodes to the zero DriveID.
func TestLayoutFromProtoDriveIDs(t *testing.T) {
	want := fixtureDriveID(7)
	got, err := LayoutFromProto(&uniov1.ErasureLayout{Drives: [][]byte{want[:], {}}})
	if err != nil {
		t.Fatalf("LayoutFromProto: %v", err)
	}
	if len(got.Drives) != 2 || got.Drives[0] != want || !got.Drives[1].IsZero() {
		t.Errorf("Drives = %v, want [%v, zero]", got.Drives, want)
	}
}

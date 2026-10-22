package recordpb

import (
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/google/go-cmp/cmp"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// TestUploadRoundTrip checks the round trip. Conditions are not stored, so
// the expected value has them cleared.
func TestUploadRoundTrip(t *testing.T) {
	for _, nu := range uploadFixtures() {
		t.Run(nu.name, func(t *testing.T) {
			p, err := UploadToProto(nu.rec)
			if err != nil {
				t.Fatalf("UploadToProto: %v", err)
			}
			got, err := UploadFromProto(p)
			if err != nil {
				t.Fatalf("UploadFromProto: %v", err)
			}
			want := nu.rec
			want.Options.Conditions = object.Conditions{}
			if diff := cmp.Diff(want, got, equivOpts()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestUploadChecksumAlgorithmMapping checks every checksum algorithm the
// upload options can request.
func TestUploadChecksumAlgorithmMapping(t *testing.T) {
	for _, a := range allChecksumAlgorithms() {
		in := meta.UploadRecord{Bucket: "b", Key: "k", Options: object.PutOptions{ChecksumAlgorithm: a}}
		p, err := UploadToProto(in)
		if err != nil {
			t.Fatalf("UploadToProto(%v): %v", a, err)
		}
		got, err := UploadFromProto(p)
		if err != nil {
			t.Fatalf("UploadFromProto(%v): %v", a, err)
		}
		if got.Options.ChecksumAlgorithm != a {
			t.Errorf("ChecksumAlgorithm = %v, want %v", got.Options.ChecksumAlgorithm, a)
		}
	}
}

// TestUploadDropsConditions checks that Conditions are absent from the
// stored message and that decoding yields zero Conditions.
func TestUploadDropsConditions(t *testing.T) {
	in := meta.UploadRecord{
		Bucket: "b",
		Key:    "k",
		Options: object.PutOptions{
			Conditions: object.Conditions{IfNoneMatch: "*", IfModifiedSince: fixtureTime()},
		},
	}
	p, err := UploadToProto(in)
	if err != nil {
		t.Fatalf("UploadToProto: %v", err)
	}
	got, err := UploadFromProto(p)
	if err != nil {
		t.Fatalf("UploadFromProto: %v", err)
	}
	if !got.Options.Conditions.IsZero() {
		t.Errorf("decoded Conditions = %+v, want zero", got.Options.Conditions)
	}
}

func TestUploadToProtoErrors(t *testing.T) {
	badAlg := meta.UploadRecord{Bucket: "b", Options: object.PutOptions{ChecksumAlgorithm: object.ChecksumAlgorithm(6)}}
	badTime := meta.UploadRecord{Bucket: "b", Initiated: time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)}

	tests := []struct {
		name string
		in   meta.UploadRecord
	}{
		{"unknown checksum algorithm", badAlg},
		{"initiated time after the nanosecond range", badTime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := UploadToProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestUploadFromProtoErrors(t *testing.T) {
	tests := []struct {
		name string
		in   *uniov1.UploadRecord
	}{
		{"nil message", nil},
		{"unknown checksum algorithm", &uniov1.UploadRecord{Bucket: "b", ChecksumAlgorithm: 6}},
		{"negative checksum algorithm", &uniov1.UploadRecord{Bucket: "b", ChecksumAlgorithm: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := UploadFromProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

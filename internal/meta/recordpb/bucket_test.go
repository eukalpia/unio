package recordpb

import (
	"math"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
	"github.com/google/go-cmp/cmp"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

func TestBucketRoundTrip(t *testing.T) {
	for _, nb := range bucketFixtures() {
		t.Run(nb.name, func(t *testing.T) {
			p, err := BucketToProto(nb.info, nb.deleting)
			if err != nil {
				t.Fatalf("BucketToProto: %v", err)
			}
			info, deleting, err := BucketFromProto(p)
			if err != nil {
				t.Fatalf("BucketFromProto: %v", err)
			}
			if diff := cmp.Diff(nb.info, info, equivOpts()); diff != "" {
				t.Errorf("info mismatch (-want +got):\n%s", diff)
			}
			if deleting != nb.deleting {
				t.Errorf("deleting = %v, want %v", deleting, nb.deleting)
			}
		})
	}
}

func TestBucketVersioningMapping(t *testing.T) {
	tests := []struct {
		state object.VersioningState
		want  uniov1.VersioningState
	}{
		{object.VersioningUnversioned, uniov1.VersioningState_VERSIONING_STATE_UNVERSIONED},
		{object.VersioningEnabled, uniov1.VersioningState_VERSIONING_STATE_ENABLED},
		{object.VersioningSuspended, uniov1.VersioningState_VERSIONING_STATE_SUSPENDED},
	}
	for _, tt := range tests {
		p, err := BucketToProto(object.BucketInfo{Versioning: tt.state}, false)
		if err != nil {
			t.Fatalf("BucketToProto(%v): %v", tt.state, err)
		}
		if p.GetVersioning() != tt.want {
			t.Errorf("versioning %v encoded as %v, want %v", tt.state, p.GetVersioning(), tt.want)
		}
	}
}

// TestBucketIDIsFullWidth checks that the full uint64 bucket id survives.
func TestBucketIDIsFullWidth(t *testing.T) {
	in := object.BucketInfo{Name: "b", ID: ids.BucketID(math.MaxUint64)}
	p, err := BucketToProto(in, false)
	if err != nil {
		t.Fatalf("BucketToProto: %v", err)
	}
	info, _, err := BucketFromProto(p)
	if err != nil {
		t.Fatalf("BucketFromProto: %v", err)
	}
	if info.ID != in.ID {
		t.Errorf("ID = %d, want %d", info.ID, in.ID)
	}
}

func TestBucketToProtoErrors(t *testing.T) {
	tests := []struct {
		name     string
		info     object.BucketInfo
		deleting bool
	}{
		{"unknown versioning state", object.BucketInfo{Versioning: object.VersioningState(3)}, false},
		{"created after the nanosecond range", object.BucketInfo{Created: time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BucketToProto(tt.info, tt.deleting); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestBucketFromProtoErrors(t *testing.T) {
	tests := []struct {
		name string
		in   *uniov1.BucketRecord
	}{
		{"nil message", nil},
		{"unknown versioning state", &uniov1.BucketRecord{Name: "b", Versioning: 3}},
		{"negative versioning state", &uniov1.BucketRecord{Name: "b", Versioning: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := BucketFromProto(tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

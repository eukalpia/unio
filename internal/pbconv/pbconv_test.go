package pbconv

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/eukalpia/unio/internal/core"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

func sampleLayout() core.ErasureLayout {
	return core.ErasureLayout{
		Set: 3, DataShards: 4, ParityShards: 2, StripeSize: 1 << 20,
		Drives: []core.DriveID{core.NewDriveID(), {}, core.NewDriveID(), core.NewDriveID(), core.NewDriveID(), core.NewDriveID()},
	}
}

func TestObjectRoundTrip(t *testing.T) {
	now := time.Unix(1700000000, 123456789).UTC()
	cases := map[string]core.ObjectRecord{
		"inline": {
			Bucket: "b", Key: "k", ModTime: now, Size: 3, ETag: "abc",
			ContentType: "text/plain", UserMetadata: map[string]string{"a": "1"},
			Checksum: core.Checksum{Algorithm: core.ChecksumCRC32C, Value: []byte{1, 2, 3, 4}},
			Inline:   []byte("abc"),
		},
		"empty-inline": {Bucket: "b", Key: "empty", ModTime: now, ETag: "d41d8cd98f00b204e9800998ecf8427e", Inline: []byte{}},
		"erasure": {
			Bucket: "b", Key: "big", VersionID: "v1", ModTime: now, Size: 10 << 20, ETag: "e",
			Parts: []core.PartRecord{{Number: 0, Size: 10 << 20, ETag: "e", ModTime: now, Data: core.NewDataID(), Layout: sampleLayout()}},
		},
		"multipart": {
			Bucket: "b", Key: "mp", ModTime: now, Size: 15 << 20, ETag: "x-2", Multipart: true,
			Checksum: core.Checksum{Algorithm: core.ChecksumSHA256, Type: core.ChecksumComposite, Value: make([]byte, 32), Parts: 2},
			Parts: []core.PartRecord{
				{Number: 1, Size: 10 << 20, ETag: "p1", ModTime: now, Data: core.NewDataID(), Layout: sampleLayout()},
				{Number: 2, Size: 5 << 20, ETag: "p2", ModTime: now, Data: core.NewDataID(), Layout: sampleLayout()},
			},
		},
		"delete-marker": {Bucket: "b", Key: "gone", VersionID: "v2", ModTime: now, DeleteMarker: true},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := ObjectToPB(in).MarshalVT()
			if err != nil {
				t.Fatal(err)
			}
			var p uniov1.ObjectRecord
			if err := p.UnmarshalVT(raw); err != nil {
				t.Fatal(err)
			}
			out, err := ObjectFromPB(&p)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(in, out, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("round trip mismatch (-in +out):\n%s", diff)
			}
			if (in.Inline == nil) != (out.Inline == nil) {
				t.Fatalf("inline presence changed: in nil=%v out nil=%v", in.Inline == nil, out.Inline == nil)
			}
		})
	}
}

func TestUploadRoundTrip(t *testing.T) {
	in := core.UploadRecord{
		Bucket: "b", Key: "k", UploadID: "u1", Initiated: time.Unix(5, 0).UTC(),
		Options: core.PutObjectOptions{ContentType: "a/b", Tags: "x=y", ChecksumAlgorithm: core.ChecksumCRC64NVME},
	}
	out := UploadFromPB(UploadToPB(in))
	if diff := cmp.Diff(in, out, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("mismatch:\n%s", diff)
	}
}

func TestBucketRoundTrip(t *testing.T) {
	in := core.BucketInfo{Name: "bkt", ID: 42, Created: time.Unix(9, 0).UTC(), Owner: "root", Versioning: core.VersioningEnabled}
	out, deleting := BucketFromPB(BucketToPB(in, true))
	if !deleting {
		t.Fatal("deleting flag lost")
	}
	if diff := cmp.Diff(in, out); diff != "" {
		t.Fatalf("mismatch:\n%s", diff)
	}
}

func TestClusterMapRoundTrip(t *testing.T) {
	d1, d2 := core.NewDriveID(), core.NewDriveID()
	in := &core.ClusterMap{
		Version: 7, ClusterID: "c1",
		Nodes:      []core.NodeInfo{{ID: "n1", Addr: "n1:9100", S3Addr: "n1:9000"}},
		Drives:     []core.DriveLocation{{ID: d1, Node: "n1", Path: "/d1"}, {ID: d2, Node: "n1", Path: "/d2"}},
		Sets:       []core.SetInfo{{ID: 0, DataShards: 1, ParityShards: 1, Drives: []core.DriveID{d1, d2}}},
		Partitions: []core.PartitionInfo{{ID: 0, Replicas: []core.NodeID{"n1"}, NonVoters: []core.NodeID{"n2"}}, {ID: 1, Replicas: []core.NodeID{"n1"}}},
	}
	out, err := ClusterMapFromPB(ClusterMapToPB(in))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(in, out, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("mismatch:\n%s", diff)
	}
}

func TestLayoutRejectsBadDriveID(t *testing.T) {
	if _, err := LayoutFromPB(&uniov1.ErasureLayout{Drives: [][]byte{{1, 2, 3}}}); err == nil {
		t.Fatal("expected error for short drive id")
	}
}

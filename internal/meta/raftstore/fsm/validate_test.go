package fsm

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// dataID returns a 16-byte DataID with the given first byte.
func dataID(first byte) []byte {
	id := make([]byte, 16)
	id[0] = first
	id[15] = 1
	return id
}

// goodLayout returns a layout that validation accepts: 2 data shards, 1
// parity shard, three drives, all written.
func goodLayout() *pb.ErasureLayout {
	return &pb.ErasureLayout{
		Set:          1,
		DataShards:   2,
		ParityShards: 1,
		StripeSize:   1 << 20,
		Drives:       [][]byte{dataID(1), dataID(2), dataID(3)},
	}
}

// goodPart returns a multipart part that validation accepts.
func goodPart(number uint32) *pb.PartRecord {
	return &pb.PartRecord{Number: number, Size: 5, Etag: "etag", Data: dataID(byte(number)), Layout: goodLayout()}
}

// rejectCase is one invalid command and the store it is applied to.
type rejectCase struct {
	name string
	kind Kind
	cmd  func() *pb.Command
}

// withRecord returns a copy of the record fixture with mutate applied.
func withRecord(mutate func(*pb.ObjectRecord)) *pb.ObjectRecord {
	rec := objRec("key", "", "etag")
	mutate(rec)
	return rec
}

// partsCmd returns a PutObject command whose record carries the given parts.
func partsCmd(parts ...*pb.PartRecord) *pb.Command {
	return putCmd(1, withRecord(func(r *pb.ObjectRecord) { r.Parts = parts }), nil)
}

// rejectCases lists commands that validation must reject. Each one would
// otherwise write, so a rejection that is really a state check would not
// explain the test.
func rejectCases() []rejectCase {
	return []rejectCase{
		{"nil command", KindData, func() *pb.Command { return &pb.Command{} }},
		{"put: bucket id zero", KindData, func() *pb.Command {
			return putCmd(0, objRec("key", "", "e"), nil)
		}},
		{"put: nil record", KindData, func() *pb.Command { return putCmd(1, nil, nil) }},
		{"put: empty key", KindData, func() *pb.Command { return putCmd(1, objRec("", "", "e"), nil) }},
		{"put: key over the limit", KindData, func() *pb.Command {
			return putCmd(1, objRec(strings.Repeat("a", object.MaxKeyLength+1), "", "e"), nil)
		}},
		{"put: key is not UTF-8", KindData, func() *pb.Command { return putCmd(1, objRec("\xff", "", "e"), nil) }},
		{"put: key contains NUL", KindData, func() *pb.Command { return putCmd(1, objRec("a\x00b", "", "e"), nil) }},
		{"put: invalid bucket name in record", KindData, func() *pb.Command {
			return putCmd(1, withRecord(func(r *pb.ObjectRecord) { r.Bucket = "Bad_Name" }), nil)
		}},
		{"put: version id in an unversioned bucket", KindData, func() *pb.Command {
			return putCmd(1, objRec("key", strings.Repeat("v", 65), "e"), nil)
		}},
		{"put: inline data over the threshold", KindData, func() *pb.Command {
			return putCmd(1, withRecord(func(r *pb.ObjectRecord) {
				r.Inline = make([]byte, object.MaxInlineThreshold+1)
			}), nil)
		}},
		{"put: inline data together with parts", KindData, func() *pb.Command {
			return putCmd(1, withRecord(func(r *pb.ObjectRecord) {
				r.Inline = []byte("x")
				r.Parts = []*pb.PartRecord{goodPart(1)}
			}), nil)
		}},
		{"put: part number above the maximum", KindData, func() *pb.Command {
			p := goodPart(object.MaxPartNumber + 1)
			return partsCmd(p)
		}},
		{"put: parts out of order", KindData, func() *pb.Command {
			return partsCmd(goodPart(2), goodPart(1))
		}},
		{"put: duplicate part numbers", KindData, func() *pb.Command {
			return partsCmd(goodPart(1), goodPart(1))
		}},
		{"put: part data is not a DataID", KindData, func() *pb.Command {
			p := goodPart(1)
			p.Data = []byte{1, 2, 3}
			return partsCmd(p)
		}},
		{"put: part without a layout", KindData, func() *pb.Command {
			p := goodPart(1)
			p.Layout = nil
			return partsCmd(p)
		}},
		{"put: layout drives do not match the shard count", KindData, func() *pb.Command {
			p := goodPart(1)
			p.Layout.Drives = [][]byte{dataID(1), dataID(2)}
			return partsCmd(p)
		}},
		{"put: layout below the write quorum", KindData, func() *pb.Command {
			p := goodPart(1)
			p.Layout.Drives = [][]byte{dataID(1), nil, nil}
			return partsCmd(p)
		}},
		{"put: layout without data shards", KindData, func() *pb.Command {
			p := goodPart(1)
			p.Layout.DataShards = 0
			p.Layout.Drives = [][]byte{dataID(1)}
			return partsCmd(p)
		}},
		{"put: drive id is not a DriveID", KindData, func() *pb.Command {
			p := goodPart(1)
			p.Layout.Drives = [][]byte{dataID(1), {1}, dataID(3)}
			return partsCmd(p)
		}},
		{"put: user metadata over the limit", KindData, func() *pb.Command {
			return putCmd(1, withRecord(func(r *pb.ObjectRecord) {
				r.Attributes = &pb.ObjectAttributes{UserMetadata: map[string]string{"k": strings.Repeat("v", object.MaxUserMetadataSize)}}
			}), nil)
		}},
		{"put: checksum algorithm out of range", KindData, func() *pb.Command {
			return putCmd(1, withRecord(func(r *pb.ObjectRecord) {
				r.Checksum = &pb.Checksum{Algorithm: 99}
			}), nil)
		}},
		{"delete: empty key", KindData, func() *pb.Command { return deleteCmd(1, "", "", "", nil) }},
		{"delete: bucket id zero", KindData, func() *pb.Command { return deleteCmd(0, "key", "", "", nil) }},
		{"delete: version id over the limit", KindData, func() *pb.Command {
			return deleteCmd(1, "key", strings.Repeat("v", 65), "", nil)
		}},
		{"delete: invalid bucket name", KindData, func() *pb.Command {
			cmd := deleteCmd(1, "key", "", "", nil)
			cmd.GetDeleteObject().Bucket = "A"
			return cmd
		}},
		{"update layout: part number above the maximum", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_UpdateLayout{UpdateLayout: &pb.UpdateLayout{
				BucketId: 1, Key: "key", PartNumber: object.MaxPartNumber + 1, Data: dataID(1), Layout: goodLayout(),
			}}}
		}},
		{"update layout: data is not a DataID", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_UpdateLayout{UpdateLayout: &pb.UpdateLayout{
				BucketId: 1, Key: "key", Data: []byte{1}, Layout: goodLayout(),
			}}}
		}},
		{"update layout: missing layout", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_UpdateLayout{UpdateLayout: &pb.UpdateLayout{
				BucketId: 1, Key: "key", Data: dataID(1),
			}}}
		}},
		{"create upload: nil upload", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CreateUpload{CreateUpload: &pb.CreateUpload{BucketId: 1}}}
		}},
		{"create upload: empty upload id", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CreateUpload{CreateUpload: &pb.CreateUpload{
				BucketId: 1, Upload: &pb.UploadRecord{Bucket: "bkt", Key: "key"},
			}}}
		}},
		{"create upload: upload id over the limit", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CreateUpload{CreateUpload: &pb.CreateUpload{
				BucketId: 1, Upload: &pb.UploadRecord{Bucket: "bkt", Key: "key", UploadId: strings.Repeat("u", 65)},
			}}}
		}},
		{"create upload: empty key", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CreateUpload{CreateUpload: &pb.CreateUpload{
				BucketId: 1, Upload: &pb.UploadRecord{Bucket: "bkt", UploadId: "u"},
			}}}
		}},
		{"put part: nil part", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{BucketId: 1, Key: "key", UploadId: "u"}}}
		}},
		{"put part: number zero", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{
				BucketId: 1, Key: "key", UploadId: "u", Part: goodPart(0),
			}}}
		}},
		{"put part: number above the maximum", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{
				BucketId: 1, Key: "key", UploadId: "u", Part: goodPart(object.MaxPartNumber + 1),
			}}}
		}},
		{"put part: data is not a DataID", KindData, func() *pb.Command {
			p := goodPart(1)
			p.Data = nil
			return &pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{
				BucketId: 1, Key: "key", UploadId: "u", Part: p,
			}}}
		}},
		{"put part: empty upload id", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{
				BucketId: 1, Key: "key", Part: goodPart(1),
			}}}
		}},
		{"complete upload: nil record", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CompleteUpload{CompleteUpload: &pb.CompleteUpload{
				BucketId: 1, Key: "key", UploadId: "u",
			}}}
		}},
		{"complete upload: empty upload id", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CompleteUpload{CompleteUpload: &pb.CompleteUpload{
				BucketId: 1, Key: "key", Record: objRec("key", "", "e"),
			}}}
		}},
		{"complete upload: part number zero", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CompleteUpload{CompleteUpload: &pb.CompleteUpload{
				BucketId: 1, Key: "key", UploadId: "u",
				Record: withRecord(func(r *pb.ObjectRecord) { r.Parts = []*pb.PartRecord{goodPart(0)} }),
			}}}
		}},
		{"complete upload: more parts than the maximum", KindData, func() *pb.Command {
			parts := make([]*pb.PartRecord, 0, object.MaxPartNumber+1)
			for n := uint32(1); n <= object.MaxPartNumber+1; n++ {
				parts = append(parts, goodPart(n))
			}
			return &pb.Command{Cmd: &pb.Command_CompleteUpload{CompleteUpload: &pb.CompleteUpload{
				BucketId: 1, Key: "key", UploadId: "u",
				Record: withRecord(func(r *pb.ObjectRecord) { r.Parts = parts }),
			}}}
		}},
		{"abort upload: empty upload id", KindData, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_AbortUpload{AbortUpload: &pb.AbortUpload{BucketId: 1, Key: "key"}}}
		}},
		{"fence: bucket id zero", KindData, func() *pb.Command { return fenceCmd(0, true) }},
		{"set partition versioning: unknown state", KindData, func() *pb.Command {
			return versioningCmd(1, pb.VersioningState(7))
		}},
		{"create bucket: empty name", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{Owner: "o"}}}
		}},
		{"create bucket: name too short", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{Name: "ab", Owner: "o"}}}
		}},
		{"create bucket: name with upper case", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{Name: "Bucket", Owner: "o"}}}
		}},
		{"create bucket: unknown versioning state", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{
				Name: "bucket", Owner: "o", Versioning: pb.VersioningState(9),
			}}}
		}},
		{"mark deleting: invalid name", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_MarkBucketDeleting{MarkBucketDeleting: &pb.MarkBucketDeleting{
				Name: "x", BucketId: 1, Deleting: true,
			}}}
		}},
		{"mark deleting: bucket id zero", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_MarkBucketDeleting{MarkBucketDeleting: &pb.MarkBucketDeleting{
				Name: "bucket", Deleting: true,
			}}}
		}},
		{"delete bucket: invalid name", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_DeleteBucket{DeleteBucket: &pb.DeleteBucket{Name: "BAD", BucketId: 1}}}
		}},
		{"set bucket versioning: unknown state", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_SetBucketVersioning{SetBucketVersioning: &pb.SetBucketVersioning{
				Name: "bucket", BucketId: 1, Versioning: pb.VersioningState(3),
			}}}
		}},
		{"kv put: empty key", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_KvPut{KvPut: &pb.KVPut{Value: []byte("v")}}}
		}},
		{"kv put: key over the limit", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_KvPut{KvPut: &pb.KVPut{
				Key: strings.Repeat("k", maxSystemKeyLength+1), Value: []byte("v"),
			}}}
		}},
		{"kv put: value over the limit", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_KvPut{KvPut: &pb.KVPut{
				Key: "k", Value: make([]byte, meta.MaxSystemValueSize+1),
			}}}
		}},
		{"kv delete: empty key", KindSystem, func() *pb.Command {
			return &pb.Command{Cmd: &pb.Command_KvDelete{KvDelete: &pb.KVDelete{}}}
		}},
	}
}

// seedState gives a harness the state that the rejection cases run against:
// one object in bucket 1 for the data group, one bucket for the system group.
func seedState(t *testing.T, h *harness) {
	t.Helper()
	if h.kind == KindSystem {
		h.mustApply(&pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{Name: "seedbkt", Owner: "o"}}})
		return
	}
	h.mustApply(putCmd(1, objRec("seed", "", "e"), nil))
}

// TestInvalidCommandsAreRejectedWithoutStateChange checks that every invalid
// command is rejected, leaves the visible state unchanged, still advances the
// applied index, and does not panic.
func TestInvalidCommandsAreRejectedWithoutStateChange(t *testing.T) {
	for _, tc := range rejectCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.kind)
			seedState(t, h)
			before := h.dumpState()
			out := h.do(tc.cmd())
			if out.Err == nil {
				t.Fatal("invalid command was accepted")
			}
			h.assertUnchanged(before)
			if got := h.f.Applied(); got != h.index {
				t.Fatalf("applied index = %d, want %d", got, h.index)
			}
			if n := h.f.PanicCount(); n != 0 {
				t.Fatalf("command panicked %d times", n)
			}
			// The store still accepts valid work after the rejection.
			if h.kind == KindSystem {
				h.mustApply(&pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{Name: "afterbkt", Owner: "o"}}})
			} else {
				h.mustApply(putCmd(1, objRec("after", "", "e"), nil))
			}
		})
	}
}

// TestMalformedReadsAreRejected checks that read queries with missing
// sub-messages or invalid fields fail with an error instead of panicking.
func TestMalformedReadsAreRejected(t *testing.T) {
	h := newHarness(t, KindData)
	seedState(t, h)
	queries := []*pb.ReadQuery{
		{},
		{Query: &pb.ReadQuery_GetObject{}},
		{Query: &pb.ReadQuery_GetUpload{}},
		{Query: &pb.ReadQuery_ListParts{}},
		{Query: &pb.ReadQuery_ListObjects{}},
		{Query: &pb.ReadQuery_ListUploads{}},
		{Query: &pb.ReadQuery_BucketHasData{}},
		{Query: &pb.ReadQuery_ListObjects{ListObjects: &pb.ListObjectsQuery{BucketId: 1, Limit: 0}}},
		{Query: &pb.ReadQuery_ListObjects{ListObjects: &pb.ListObjectsQuery{BucketId: 1, Limit: maxReadLimit + 1}}},
		{Query: &pb.ReadQuery_ListUploads{ListUploads: &pb.ListUploadsQuery{BucketId: 1, Limit: maxReadLimit + 1}}},
		{Query: &pb.ReadQuery_GetObject{GetObject: &pb.GetObjectQuery{BucketId: 1, Key: strings.Repeat("a", object.MaxKeyLength+1)}}},
	}
	for i, q := range queries {
		if _, err := h.f.Read(q); err == nil {
			t.Errorf("query %d (%v) was accepted", i, q)
		}
	}
	if n := h.f.PanicCount(); n != 0 {
		t.Fatalf("reads panicked %d times", n)
	}
}

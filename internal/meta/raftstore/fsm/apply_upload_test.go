package fsm

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// partData returns a 16-byte DataID filled with n.
func partData(n byte) []byte { return bytes.Repeat([]byte{n}, 16) }

func createUploadCmd(bucket ids.BucketID, key, uploadID string) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_CreateUpload{CreateUpload: &pb.CreateUpload{
		BucketId: uint64(bucket),
		Upload: &pb.UploadRecord{
			Bucket:            "bkt",
			Key:               key,
			UploadId:          uploadID,
			InitiatedUnixNano: 1,
		},
	}}}
}

func putPartCmd(bucket ids.BucketID, key, uploadID string, number uint32, etag string, data []byte) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{
		BucketId: uint64(bucket),
		Key:      key,
		UploadId: uploadID,
		Part: &pb.PartRecord{
			Layout:          goodLayout(),
			Number:          number,
			Size:            int64(number) * 100,
			Etag:            etag,
			Data:            data,
			ModTimeUnixNano: 1,
		},
	}}}
}

// completeCmd builds a CompleteUpload whose record lists the given parts,
// each carrying only its number and DataID.
func completeCmd(bucket ids.BucketID, key, uploadID, versionID string, cond *pb.WriteConditions, parts ...*pb.PartRecord) *pb.Command {
	rec := objRec(key, versionID, "")
	rec.Multipart = true
	rec.Parts = parts
	return &pb.Command{Cmd: &pb.Command_CompleteUpload{CompleteUpload: &pb.CompleteUpload{
		BucketId:   uint64(bucket),
		Key:        key,
		UploadId:   uploadID,
		Record:     rec,
		Conditions: cond,
	}}}
}

func abortCmd(bucket ids.BucketID, key, uploadID string) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_AbortUpload{AbortUpload: &pb.AbortUpload{
		BucketId: uint64(bucket),
		Key:      key,
		UploadId: uploadID,
	}}}
}

// listedPart names a part for a CompleteUpload record: its number, its DataID,
// and the etag that putPartCmd stored with that number.
func listedPart(number uint32, data []byte) *pb.PartRecord {
	return &pb.PartRecord{Number: number, Data: data, Etag: fmt.Sprintf("p%d", number), Layout: goodLayout()}
}

// uploadPartKeys counts the stored part keys of an upload.
func (h *harness) uploadPartKeys(uploadID string) int {
	h.t.Helper()
	prefix := keys.PartUploadPrefix(testBucket, []byte(uploadID))
	it, err := h.f.prefixIter(prefix)
	if err != nil {
		h.t.Fatalf("iterate: %v", err)
	}
	n := 0
	for ok := it.First(); ok; ok = it.Next() {
		n++
	}
	if err := closeIter(it, it.Error()); err != nil {
		h.t.Fatalf("iterate: %v", err)
	}
	return n
}

// uploadExists reports whether the upload is listed by GetUpload.
func (h *harness) uploadExists(key, uploadID string) bool {
	h.t.Helper()
	_, err := h.f.Read(&pb.ReadQuery{Query: &pb.ReadQuery_GetUpload{GetUpload: &pb.GetUploadQuery{
		BucketId: uint64(testBucket),
		Key:      key,
		UploadId: uploadID,
	}}})
	if err != nil {
		if s3err.CodeOf(err) == s3err.NoSuchUpload {
			return false
		}
		h.t.Fatalf("get upload: %v", err)
	}
	return true
}

func TestCreateUpload(t *testing.T) {
	h := newHarness(t, KindData)

	h.mustApply(createUploadCmd(testBucket, "k", "u1"))
	if !h.uploadExists("k", "u1") {
		t.Fatal("upload not created")
	}

	before := h.dumpState()
	h.mustReject(createUploadCmd(testBucket, "k", "u1"))
	h.assertUnchanged(before)

	h.mustReject(createUploadCmd(testBucket, "k", ""))
}

// An upload id is free again once its upload completes or aborts.
func TestUploadIDIsReleasedOnCompleteAndAbort(t *testing.T) {
	h := newHarness(t, KindData)

	h.mustApply(createUploadCmd(testBucket, "k", "u1"))
	h.mustApply(abortCmd(testBucket, "k", "u1"))
	h.mustApply(createUploadCmd(testBucket, "k", "u1"))

	h.mustApply(putPartCmd(testBucket, "k", "u1", 1, "p1", partData(1)))
	h.mustApply(completeCmd(testBucket, "k", "u1", "", nil, listedPart(1, partData(1))))
	h.mustApply(createUploadCmd(testBucket, "other", "u1"))
}

// The upload id names the upload in the part keyspace, which has no key
// component, so an id can never belong to two keys at once.
func TestCreateUploadRejectsIDUsedByAnotherKey(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(createUploadCmd(testBucket, "a", "shared"))
	h.mustApply(putPartCmd(testBucket, "a", "shared", 1, "pa", partData(1)))

	before := h.dumpState()
	h.mustReject(createUploadCmd(testBucket, "b", "shared"))
	h.assertUnchanged(before)
}

func TestPutPart(t *testing.T) {
	h := newHarness(t, KindData)

	before := h.dumpState()
	wantCode(t, h.mustReject(putPartCmd(testBucket, "k", "missing", 1, "p1", partData(1))), s3err.NoSuchUpload)
	h.assertUnchanged(before)

	h.mustApply(createUploadCmd(testBucket, "k", "u1"))
	res := h.mustApply(putPartCmd(testBucket, "k", "u1", 1, "p1", partData(1)))
	if res.ReplacedPart != nil {
		t.Fatalf("first part reported a replacement: %v", res.ReplacedPart)
	}

	res = h.mustApply(putPartCmd(testBucket, "k", "u1", 1, "p1b", partData(9)))
	if res.ReplacedPart == nil || res.ReplacedPart.Etag != "p1" || !bytes.Equal(res.ReplacedPart.Data, partData(1)) {
		t.Fatalf("replaced part = %v, want the first part p1", res.ReplacedPart)
	}
	if n := h.uploadPartKeys("u1"); n != 1 {
		t.Fatalf("stored parts = %d, want 1", n)
	}

	// A part for a different key of the same upload id is not this upload.
	wantCode(t, h.mustReject(putPartCmd(testBucket, "other", "u1", 2, "p2", partData(2))), s3err.NoSuchUpload)
}

func TestCompleteUpload(t *testing.T) {
	type setup struct {
		versioned bool
		existing  bool // an object exists at the key before the completion
	}
	seed := func(t *testing.T, h *harness, s setup) {
		t.Helper()
		if s.versioned {
			h.mustApply(versioningCmd(testBucket, enabled))
		}
		if s.existing {
			vid := ""
			if s.versioned {
				vid = "v0"
			}
			h.mustApply(putCmd(testBucket, objRec("k", vid, etagA), nil))
		}
		h.mustApply(createUploadCmd(testBucket, "k", "u1"))
		h.mustApply(putPartCmd(testBucket, "k", "u1", 1, "p1", partData(1)))
		h.mustApply(putPartCmd(testBucket, "k", "u1", 2, "p2", partData(2)))
		h.mustApply(putPartCmd(testBucket, "k", "u1", 3, "p3", partData(3)))
	}

	t.Run("unversioned success uses stored parts and returns the rest", func(t *testing.T) {
		h := newHarness(t, KindData)
		seed(t, h, setup{existing: true})

		res := h.mustApply(completeCmd(testBucket, "k", "u1", "", nil, listedPart(1, partData(1)), listedPart(3, partData(3))))
		if len(res.Garbage) != 1 || res.Garbage[0].Etag != etagA {
			t.Fatalf("garbage = %v, want the replaced null version", res.Garbage)
		}
		if len(res.UnusedParts) != 1 || res.UnusedParts[0].Number != 2 {
			t.Fatalf("unused parts = %v, want part 2 only", res.UnusedParts)
		}

		got, err := h.getObject("k", "")
		if err != nil {
			t.Fatalf("GetObject: %v", err)
		}
		if len(got.Parts) != 2 {
			t.Fatalf("object has %d parts, want 2", len(got.Parts))
		}
		// The stored copy carries the size and etag from PutPart; the record
		// listed only the number and DataID.
		if got.Parts[0].Etag != "p1" || got.Parts[0].Size != 100 || got.Parts[1].Etag != "p3" || got.Parts[1].Size != 300 {
			t.Fatalf("object parts = %v, want the stored copies", got.Parts)
		}

		if h.uploadExists("k", "u1") {
			t.Fatal("upload still exists after completion")
		}
		if n := h.uploadPartKeys("u1"); n != 0 {
			t.Fatalf("%d part keys remain after completion", n)
		}
	})

	t.Run("versioned success adds a version and no garbage", func(t *testing.T) {
		h := newHarness(t, KindData)
		seed(t, h, setup{versioned: true, existing: true})

		res := h.mustApply(completeCmd(testBucket, "k", "u1", "v1", nil, listedPart(1, partData(1))))
		if res.VersionId != "v1" || len(res.Garbage) != 0 {
			t.Fatalf("versioned complete: VersionId %q garbage %d; want v1 and none", res.VersionId, len(res.Garbage))
		}
		if _, err := h.getObject("k", "v1"); err != nil {
			t.Fatalf("v1 not readable: %v", err)
		}
		if _, err := h.getObject("k", ""); err != nil {
			t.Fatalf("latest not readable: %v", err)
		}
	})

	t.Run("versioned bucket requires a version id", func(t *testing.T) {
		h := newHarness(t, KindData)
		seed(t, h, setup{versioned: true})
		before := h.dumpState()
		h.mustReject(completeCmd(testBucket, "k", "u1", "", nil, listedPart(1, partData(1))))
		h.assertUnchanged(before)
	})

	rejects := []struct {
		name    string
		setup   setup
		cmd     func() *pb.Command
		wantErr s3err.Code
	}{
		{
			name:    "unknown upload",
			cmd:     func() *pb.Command { return completeCmd(testBucket, "k", "nope", "", nil, listedPart(1, partData(1))) },
			wantErr: s3err.NoSuchUpload,
		},
		{
			name: "parts out of order",
			cmd: func() *pb.Command {
				return completeCmd(testBucket, "k", "u1", "", nil, listedPart(2, partData(2)), listedPart(1, partData(1)))
			},
			wantErr: s3err.InvalidPartOrder,
		},
		{
			name: "duplicate part number",
			cmd: func() *pb.Command {
				return completeCmd(testBucket, "k", "u1", "", nil, listedPart(1, partData(1)), listedPart(1, partData(1)))
			},
			wantErr: s3err.InvalidPartOrder,
		},
		{
			name: "data mismatch",
			cmd: func() *pb.Command {
				return completeCmd(testBucket, "k", "u1", "", nil, listedPart(1, partData(1)), listedPart(2, partData(7)))
			},
			wantErr: s3err.InvalidPart,
		},
		{
			name: "part never stored",
			cmd: func() *pb.Command {
				return completeCmd(testBucket, "k", "u1", "", nil, listedPart(1, partData(1)), listedPart(9, partData(9)))
			},
			wantErr: s3err.InvalidPart,
		},
		{
			name:  "if-none-match star on a present key",
			setup: setup{existing: true},
			cmd: func() *pb.Command {
				return completeCmd(testBucket, "k", "u1", "", &pb.WriteConditions{IfNoneMatch: "*"}, listedPart(1, partData(1)))
			},
			wantErr: s3err.PreconditionFailed,
		},
		{
			name: "if-match on an absent key",
			cmd: func() *pb.Command {
				return completeCmd(testBucket, "k", "u1", "", &pb.WriteConditions{IfMatch: etagA}, listedPart(1, partData(1)))
			},
			wantErr: s3err.NoSuchKey,
		},
	}
	for _, tc := range rejects {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, KindData)
			seed(t, h, tc.setup)
			before := h.dumpState()
			wantCode(t, h.mustReject(tc.cmd()), tc.wantErr)
			h.assertUnchanged(before)
			if tc.wantErr != s3err.NoSuchUpload && !h.uploadExists("k", "u1") {
				t.Fatal("rejected completion removed the upload")
			}
		})
	}

	t.Run("completed upload cannot be completed again", func(t *testing.T) {
		h := newHarness(t, KindData)
		seed(t, h, setup{})
		h.mustApply(completeCmd(testBucket, "k", "u1", "", nil, listedPart(1, partData(1))))
		wantCode(t, h.mustReject(completeCmd(testBucket, "k", "u1", "", nil, listedPart(1, partData(1)))), s3err.NoSuchUpload)
	})
}

func TestAbortUpload(t *testing.T) {
	h := newHarness(t, KindData)

	wantCode(t, h.mustReject(abortCmd(testBucket, "k", "nope")), s3err.NoSuchUpload)

	h.mustApply(createUploadCmd(testBucket, "k", "u1"))
	h.mustApply(putPartCmd(testBucket, "k", "u1", 2, "p2", partData(2)))
	h.mustApply(putPartCmd(testBucket, "k", "u1", 1, "p1", partData(1)))

	res := h.mustApply(abortCmd(testBucket, "k", "u1"))
	if len(res.UnusedParts) != 2 || res.UnusedParts[0].Number != 1 || res.UnusedParts[1].Number != 2 {
		t.Fatalf("unused parts = %v, want parts 1 and 2 in order", res.UnusedParts)
	}
	if h.uploadExists("k", "u1") || h.uploadPartKeys("u1") != 0 {
		t.Fatal("abort left the upload or its parts behind")
	}
	wantCode(t, h.mustReject(abortCmd(testBucket, "k", "u1")), s3err.NoSuchUpload)
}

func TestFencedBucketRejectsUploadWritesButAllowsAbort(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(createUploadCmd(testBucket, "k", "u1"))
	h.mustApply(putPartCmd(testBucket, "k", "u1", 1, "p1", partData(1)))
	h.mustApply(createUploadCmd(testBucket, "k", "u2"))
	h.mustApply(fenceCmd(testBucket, true))

	wantCode(t, h.mustReject(createUploadCmd(testBucket, "k", "u3")), s3err.NoSuchBucket)
	wantCode(t, h.mustReject(putPartCmd(testBucket, "k", "u1", 2, "p2", partData(2))), s3err.NoSuchBucket)
	wantCode(t, h.mustReject(completeCmd(testBucket, "k", "u1", "", nil, listedPart(1, partData(1)))), s3err.NoSuchBucket)

	res := h.mustApply(abortCmd(testBucket, "k", "u1"))
	if len(res.UnusedParts) != 1 {
		t.Fatalf("abort in fenced bucket: unused parts %d, want 1", len(res.UnusedParts))
	}
	h.mustApply(abortCmd(testBucket, "k", "u2"))
}

// Upload ids are raw bytes in the part keyspace, and a NUL byte in an id
// must not make it collide with a shorter id.
func TestUploadIDsWithNULBytesAreDistinct(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(createUploadCmd(testBucket, "k", "a\x00b"))
	h.mustApply(createUploadCmd(testBucket, "k", "a"))
	h.mustApply(putPartCmd(testBucket, "k", "a", 1, "p", partData(1)))
	res := h.mustApply(abortCmd(testBucket, "k", "a\x00b"))
	if len(res.UnusedParts) != 0 {
		t.Fatalf("aborting a\\x00b returned parts of upload a: %v", res.UnusedParts)
	}
	if h.uploadPartKeys("a") != 1 {
		t.Fatal("aborting a\\x00b removed the part of upload a")
	}
}

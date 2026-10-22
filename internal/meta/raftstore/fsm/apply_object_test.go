package fsm

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/meta"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

const (
	etagA = "etag-a"
	etagB = "etag-b"
)

var (
	enabled   = pb.VersioningState_VERSIONING_STATE_ENABLED
	suspended = pb.VersioningState_VERSIONING_STATE_SUSPENDED
)

func TestPutObjectConditionsUnversioned(t *testing.T) {
	tests := []struct {
		name    string
		seed    bool // store etagA as the null version first
		cond    *pb.WriteConditions
		wantErr s3err.Code // empty means the write succeeds
	}{
		{name: "no conditions, absent key"},
		{name: "no conditions, present key", seed: true},
		{name: "if-none-match star, absent key", cond: &pb.WriteConditions{IfNoneMatch: "*"}},
		{name: "if-none-match star, present key", seed: true, cond: &pb.WriteConditions{IfNoneMatch: "*"}, wantErr: s3err.PreconditionFailed},
		{name: "if-match matching etag", seed: true, cond: &pb.WriteConditions{IfMatch: etagA}},
		{name: "if-match mismatching etag", seed: true, cond: &pb.WriteConditions{IfMatch: "other"}, wantErr: s3err.PreconditionFailed},
		{name: "if-match on absent key", cond: &pb.WriteConditions{IfMatch: etagA}, wantErr: s3err.NoSuchKey},
		{name: "if-match star, present key", seed: true, cond: &pb.WriteConditions{IfMatch: "*"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, KindData)
			if tc.seed {
				h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))
			}
			before := h.dumpState()
			cmd := putCmd(testBucket, objRec("k", "", etagB), tc.cond)
			if tc.wantErr != "" {
				wantCode(t, h.mustReject(cmd), tc.wantErr)
				h.assertUnchanged(before)
				return
			}
			res := h.mustApply(cmd)
			if res.VersionId != "" {
				t.Fatalf("VersionId = %q, want null", res.VersionId)
			}
			wantGarbage := 0
			if tc.seed {
				wantGarbage = 1
			}
			if len(res.Garbage) != wantGarbage {
				t.Fatalf("garbage = %d records, want %d", len(res.Garbage), wantGarbage)
			}
			if tc.seed && res.Garbage[0].Etag != etagA {
				t.Fatalf("garbage etag = %q, want the replaced %q", res.Garbage[0].Etag, etagA)
			}
			got, err := h.getObject("k", "")
			if err != nil || got.Etag != etagB {
				t.Fatalf("latest after write = %v, %v; want etag %q", got, err, etagB)
			}
			if n := len(h.versionsOf("k")); n != 1 {
				t.Fatalf("stored versions = %d, want 1 (null version replaced)", n)
			}
		})
	}
}

func TestPutObjectVersioned(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, enabled))

	res := h.mustApply(putCmd(testBucket, objRec("k", "v1", etagA), nil))
	if res.VersionId != "v1" || len(res.Garbage) != 0 {
		t.Fatalf("first put: VersionId %q garbage %d; want v1 and none", res.VersionId, len(res.Garbage))
	}

	// A versioned bucket requires an id for every write.
	before := h.dumpState()
	h.mustReject(putCmd(testBucket, objRec("k", "", etagB), nil))
	h.assertUnchanged(before)

	// Duplicate ids are rejected for the same key, and only there.
	before = h.dumpState()
	h.mustReject(putCmd(testBucket, objRec("k", "v1", etagB), nil))
	h.assertUnchanged(before)

	res = h.mustApply(putCmd(testBucket, objRec("j", "v1", etagB), nil))
	if res.VersionId != "v1" {
		t.Fatalf("same id under another key: VersionId %q, want v1", res.VersionId)
	}

	res = h.mustApply(putCmd(testBucket, objRec("k", "v2", etagB), nil))
	if len(res.Garbage) != 0 {
		t.Fatalf("versioned put returned %d garbage records, want none", len(res.Garbage))
	}
	latest, err := h.getObject("k", "")
	if err != nil || latest.VersionId != "v2" {
		t.Fatalf("latest = %v, %v; want v2", latest, err)
	}
	old, err := h.getObject("k", "v1")
	if err != nil || old.Etag != etagA {
		t.Fatalf("v1 after v2 = %v, %v; want the first etag", old, err)
	}

	// Preconditions see the latest version.
	wantCode(t, h.mustReject(putCmd(testBucket, objRec("k", "v3", etagB), &pb.WriteConditions{IfNoneMatch: "*"})), s3err.PreconditionFailed)
	h.mustApply(putCmd(testBucket, objRec("k", "v3", etagB), &pb.WriteConditions{IfMatch: etagB}))
}

func TestPutObjectSuspendedReplacesNullVersion(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, suspended))

	if res := h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil)); len(res.Garbage) != 0 {
		t.Fatalf("first null put: garbage %d, want none", len(res.Garbage))
	}
	res := h.mustApply(putCmd(testBucket, objRec("k", "", etagB), nil))
	if len(res.Garbage) != 1 || res.Garbage[0].Etag != etagA {
		t.Fatalf("second null put: garbage %v, want exactly the first null version", res.Garbage)
	}
	if n := len(h.versionsOf("k")); n != 1 {
		t.Fatalf("stored versions = %d, want 1", n)
	}

	// Suspended buckets keep the enabled-era versions and replace only the
	// null version.
	h.mustApply(versioningCmd(testBucket, enabled))
	h.mustApply(putCmd(testBucket, objRec("k", "v1", "etag-v1"), nil))
	h.mustApply(versioningCmd(testBucket, suspended))
	res = h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))
	if len(res.Garbage) != 1 || res.Garbage[0].Etag != etagB {
		t.Fatalf("null put over enabled-era history: garbage %v, want the old null version", res.Garbage)
	}
	if _, err := h.getObject("k", "v1"); err != nil {
		t.Fatalf("v1 must survive a suspended write: %v", err)
	}
	if n := len(h.versionsOf("k")); n != 2 {
		t.Fatalf("stored versions = %d, want 2", n)
	}

	// A suspended bucket still rejects ids.
	h.mustReject(putCmd(testBucket, objRec("k", "v9", etagB), nil))
}

func TestDeleteObjectUnversioned(t *testing.T) {
	h := newHarness(t, KindData)

	before := h.dumpState()
	res := h.mustApply(deleteCmd(testBucket, "missing", "", "", nil))
	if res.DeleteMarker || len(res.Garbage) != 0 {
		t.Fatalf("delete of missing key: marker %v garbage %d, want none", res.DeleteMarker, len(res.Garbage))
	}
	h.assertUnchanged(before)

	h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))
	res = h.mustApply(deleteCmd(testBucket, "k", "", "", nil))
	if res.DeleteMarker {
		t.Fatal("unversioned delete must not add a delete marker")
	}
	if len(res.Garbage) != 1 || res.Garbage[0].Etag != etagA {
		t.Fatalf("garbage = %v, want the null version", res.Garbage)
	}
	if _, err := h.getObject("k", ""); err == nil {
		t.Fatal("GetObject after delete: want NoSuchKey")
	}

	// A version id that does not exist is a success with no change.
	h.mustApply(putCmd(testBucket, objRec("k", "", etagB), nil))
	before = h.dumpState()
	res = h.mustApply(deleteCmd(testBucket, "k", "no-such-id", "", nil))
	if len(res.Garbage) != 0 {
		t.Fatalf("absent version id: garbage %d, want none", len(res.Garbage))
	}
	h.assertUnchanged(before)

	// If-Match is checked against the present key: a mismatch fails, a match
	// deletes. After that the key is absent, so If-None-Match "*" passes.
	wantCode(t, h.mustReject(deleteCmd(testBucket, "k", "", "", &pb.WriteConditions{IfMatch: "wrong"})), s3err.PreconditionFailed)
	h.mustApply(deleteCmd(testBucket, "k", "", "", &pb.WriteConditions{IfMatch: etagB}))
	h.mustApply(deleteCmd(testBucket, "k", "", "", &pb.WriteConditions{IfNoneMatch: "*"}))
}

func TestDeleteObjectVersioned(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, enabled))
	h.mustApply(putCmd(testBucket, objRec("k", "v1", etagA), nil))
	h.mustApply(putCmd(testBucket, objRec("k", "v2", etagB), nil))

	// A delete without a version id adds a marker with the proposer's id.
	res := h.mustApply(deleteCmd(testBucket, "k", "", "m1", nil))
	if !res.DeleteMarker || res.DeleteMarkerVersionId != "m1" || len(res.Garbage) != 0 {
		t.Fatalf("marker delete: %+v; want marker m1 and no garbage", res)
	}
	marker, err := h.getObject("k", "")
	if err != nil || !marker.DeleteMarker || marker.VersionId != "m1" {
		t.Fatalf("latest = %v, %v; want delete marker m1", marker, err)
	}
	if _, err := h.getObject("k", "v2"); err != nil {
		t.Fatalf("v2 must survive a marker: %v", err)
	}

	// Marker ids are unique per key.
	before := h.dumpState()
	h.mustReject(deleteCmd(testBucket, "k", "", "m1", nil))
	h.assertUnchanged(before)

	// A second marker is allowed.
	h.mustApply(deleteCmd(testBucket, "k", "", "m2", nil))

	// Deleting a marker by id removes it and reports it as a marker.
	res = h.mustApply(deleteCmd(testBucket, "k", "m2", "", nil))
	if !res.DeleteMarker || res.DeleteMarkerVersionId != "m2" {
		t.Fatalf("delete of marker m2: DeleteMarker %v id %q", res.DeleteMarker, res.DeleteMarkerVersionId)
	}
	if len(res.Garbage) != 1 || res.Garbage[0].VersionId != "m2" {
		t.Fatalf("garbage = %v, want the marker m2 record", res.Garbage)
	}
	latest, err := h.getObject("k", "")
	if err != nil || latest.VersionId != "m1" {
		t.Fatalf("latest after removing m2 = %v, %v; want m1", latest, err)
	}

	// Deleting a data version reports garbage and no marker.
	res = h.mustApply(deleteCmd(testBucket, "k", "v1", "", nil))
	if res.DeleteMarker || len(res.Garbage) != 1 || res.Garbage[0].Etag != etagA {
		t.Fatalf("delete of v1: %+v; want garbage v1 and no marker", res)
	}
	_, err = h.getObject("k", "v1")
	wantCode(t, err, s3err.NoSuchVersion)

	// Deleting an absent version is a success with no change.
	before = h.dumpState()
	res = h.mustApply(deleteCmd(testBucket, "k", "v1", "", nil))
	if len(res.Garbage) != 0 || res.DeleteMarker {
		t.Fatalf("absent version: %+v, want no effect", res)
	}
	h.assertUnchanged(before)

	// Preconditions are evaluated on the latest version (m1, a marker), not
	// on the version being deleted.
	wantCode(t, h.mustReject(deleteCmd(testBucket, "k", "v2", "", &pb.WriteConditions{IfMatch: etagB})), s3err.NoSuchKey)
	h.mustApply(deleteCmd(testBucket, "k", "", "m3", &pb.WriteConditions{IfNoneMatch: "*"}))

	// A versioned bucket cannot add a marker without an id.
	before = h.dumpState()
	h.mustReject(deleteCmd(testBucket, "k", "", "", nil))
	h.assertUnchanged(before)
}

func TestDeleteObjectConditionsUseLatestVersion(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, enabled))
	h.mustApply(putCmd(testBucket, objRec("k", "v1", etagA), nil))
	h.mustApply(putCmd(testBucket, objRec("k", "v2", etagB), nil))

	// v1 is not the latest, so If-Match on v1's etag must fail and If-Match on
	// the latest etag must pass, even when deleting v1 itself.
	wantCode(t, h.mustReject(deleteCmd(testBucket, "k", "v1", "", &pb.WriteConditions{IfMatch: etagA})), s3err.PreconditionFailed)
	h.mustApply(deleteCmd(testBucket, "k", "v1", "", &pb.WriteConditions{IfMatch: etagB}))
}

func TestDeleteObjectSuspended(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, suspended))

	// A delete in a suspended bucket removes the null version and adds a null
	// delete marker.
	h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))
	res := h.mustApply(deleteCmd(testBucket, "k", "", "", nil))
	if !res.DeleteMarker || res.DeleteMarkerVersionId != "" {
		t.Fatalf("suspended delete: DeleteMarker %v id %q, want a null marker", res.DeleteMarker, res.DeleteMarkerVersionId)
	}
	if len(res.Garbage) != 1 || res.Garbage[0].Etag != etagA {
		t.Fatalf("garbage = %v, want the null version", res.Garbage)
	}
	marker, err := h.getObject("k", "")
	if err != nil || !marker.DeleteMarker || marker.VersionId != "" {
		t.Fatalf("latest = %v, %v; want null delete marker", marker, err)
	}

	// Deleting again replaces the null marker, which is garbage.
	res = h.mustApply(deleteCmd(testBucket, "k", "", "", nil))
	if !res.DeleteMarker || len(res.Garbage) != 1 || !res.Garbage[0].DeleteMarker {
		t.Fatalf("second suspended delete: %+v, want the old null marker as garbage", res)
	}
	if n := len(h.versionsOf("k")); n != 1 {
		t.Fatalf("stored versions = %d, want 1 null marker", n)
	}

	// A missing key still gets a null marker.
	res = h.mustApply(deleteCmd(testBucket, "absent", "", "", nil))
	if !res.DeleteMarker || len(res.Garbage) != 0 {
		t.Fatalf("suspended delete of absent key: %+v, want a marker and no garbage", res)
	}
}

func TestFencedBucketRejectsWritesButAllowsDeletes(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))
	h.mustApply(fenceCmd(testBucket, true))

	wantCode(t, h.mustReject(putCmd(testBucket, objRec("k", "", etagB), nil)), s3err.NoSuchBucket)
	res := h.mustApply(deleteCmd(testBucket, "k", "", "", nil))
	if len(res.Garbage) != 1 {
		t.Fatalf("delete in fenced bucket: garbage %d, want 1", len(res.Garbage))
	}

	h.mustApply(fenceCmd(testBucket, false))
	h.mustApply(putCmd(testBucket, objRec("k", "", etagB), nil))
}

func TestUpdateLayout(t *testing.T) {
	dataV1P1 := bytes.Repeat([]byte{1}, 16)
	dataV1P2 := bytes.Repeat([]byte{2}, 16)
	dataV2P1 := bytes.Repeat([]byte{3}, 16)
	newLayout := &pb.ErasureLayout{Set: 7, DataShards: 4, ParityShards: 2}

	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, enabled))
	multipart := func(version string, p1 []byte) *pb.ObjectRecord {
		rec := objRec("k", version, etagA)
		rec.Multipart = true
		rec.Parts = []*pb.PartRecord{
			{Number: 1, Size: 5, Etag: "p1", Data: p1, Layout: &pb.ErasureLayout{Set: 1}},
			{Number: 2, Size: 6, Etag: "p2", Data: dataV1P2, Layout: &pb.ErasureLayout{Set: 1}},
		}
		return rec
	}
	h.mustApply(putCmd(testBucket, multipart("v1", dataV1P1), nil))
	h.mustApply(putCmd(testBucket, multipart("v2", dataV2P1), nil))
	h.mustApply(deleteCmd(testBucket, "gone", "", "m1", nil)) // a key whose latest is a marker

	update := func(key, version string, part uint32, data []byte) *pb.Command {
		return &pb.Command{Cmd: &pb.Command_UpdateLayout{UpdateLayout: &pb.UpdateLayout{
			BucketId:   uint64(testBucket),
			Key:        key,
			VersionId:  version,
			PartNumber: part,
			Data:       data,
			Layout:     newLayout,
		}}}
	}

	t.Run("latest by empty version id", func(t *testing.T) {
		res := h.mustApply(update("k", "", 1, dataV2P1))
		if len(res.Garbage) != 0 {
			t.Fatalf("UpdateLayout returned garbage: %v", res.Garbage)
		}
		v2, err := h.getObject("k", "v2")
		if err != nil || v2.Parts[0].Layout.Set != 7 {
			t.Fatalf("v2 part 1 layout after update = %v, %v; want set 7", v2, err)
		}
		v1, err := h.getObject("k", "v1")
		if err != nil || v1.Parts[0].Layout.Set != 1 {
			t.Fatalf("v1 changed by an update to v2: %v, %v", v1, err)
		}
		if v2.Parts[1].Layout.Set != 1 {
			t.Fatal("update changed the other part")
		}
	})

	t.Run("explicit version id", func(t *testing.T) {
		h.mustApply(update("k", "v1", 1, dataV1P1))
		v1, err := h.getObject("k", "v1")
		if err != nil || v1.Parts[0].Layout.Set != 7 {
			t.Fatalf("v1 part 1 after update = %v, %v; want set 7", v1, err)
		}
	})

	rejects := []struct {
		name string
		cmd  *pb.Command
	}{
		{"data mismatch", update("k", "v1", 1, dataV2P1)},
		{"unknown part", update("k", "v1", 9, dataV1P1)},
		{"unknown version", update("k", "v9", 1, dataV1P1)},
		{"latest is a delete marker", update("gone", "", 1, dataV1P1)},
		{"unknown key", update("nope", "", 1, dataV1P1)},
	}
	for _, tc := range rejects {
		t.Run(tc.name, func(t *testing.T) {
			before := h.dumpState()
			err := h.mustReject(tc.cmd)
			if !errors.Is(err, meta.ErrRevisionMismatch) {
				t.Fatalf("error = %v, want ErrRevisionMismatch", err)
			}
			h.assertUnchanged(before)
		})
	}
}

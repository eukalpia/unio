package metatest

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// layoutCases covers UpdateLayout, which repair uses to move a part to a new
// set of drives.
func layoutCases() []storeCase {
	return []storeCase{
		{"Layout/UpdateReplacesLayout", caseUpdateLayoutUnversioned},
		{"Layout/UpdateVersionedObject", caseUpdateLayoutVersioned},
		{"Layout/UpdateRevisionMismatch", caseUpdateLayoutMismatch},
		{"Layout/UpdateMissingBucket", caseUpdateLayoutMissingBucket},
	}
}

// singlePartRecord returns a non-inline object of one part (number 0), whose
// data id is the returned part's Data.
func singlePartRecord(bucket, key, versionID string, body string) (meta.ObjectRecord, meta.PartRecord) {
	part := partRecord(0, body)
	rec := objectRecord(bucket, key, versionID, body)
	rec.Inline = nil
	rec.Parts = []meta.PartRecord{part}
	return rec, part
}

// requireSameLayout fails the test unless two layouts are equal.
func requireSameLayout(t *testing.T, want, got erasure.Layout) {
	t.Helper()
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("layout mismatch (-want +got):\n%s", diff)
	}
}

func caseUpdateLayoutUnversioned(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	rec, part := singlePartRecord(testBucket, "k", "", "body")
	mustPutObject(t, s, rec)

	moved := testLayout()
	mustNoErr(t, s.UpdateLayout(t.Context(), testBucket, "k", "", 0, part.Data, moved))
	got, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireNumbers(t, "parts", got.Parts, 0)
	requireSameLayout(t, moved, got.Parts[0].Layout)
	if got.Parts[0].Data != part.Data {
		t.Fatal("UpdateLayout changed the data id")
	}
}

func caseUpdateLayoutVersioned(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	id := newVersionID()
	rec, part := singlePartRecord(testBucket, "k", id, "body")
	mustPutObject(t, s, rec)
	mustPutObject(t, s, objectRecord(testBucket, "k", newVersionID(), "newer"))

	moved := testLayout()
	mustNoErr(t, s.UpdateLayout(t.Context(), testBucket, "k", id, 0, part.Data, moved))
	got, err := s.GetObject(t.Context(), testBucket, "k", id)
	mustNoErr(t, err)
	requireSameLayout(t, moved, got.Parts[0].Layout)
}

// caseUpdateLayoutMismatch checks each condition under which UpdateLayout
// must fail with ErrRevisionMismatch, and that a failed call changes nothing.
func caseUpdateLayoutMismatch(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	single, part := singlePartRecord(testBucket, "single", newVersionID(), "body")
	mustPutObject(t, s, single)
	mustPutObject(t, s, objectRecord(testBucket, "inline", newVersionID(), "inline"))
	markerKey := "marked"
	mustPutObject(t, s, objectRecord(testBucket, markerKey, newVersionID(), "gone"))
	_, _, err := s.DeleteObject(t.Context(), testBucket, markerKey, "", object.Conditions{})
	mustNoErr(t, err)

	other := ids.NewDataID()
	tests := []struct {
		name      string
		key       string
		versionID string
		partNum   int
		data      ids.DataID
	}{
		{"wrong data id", "single", "", 0, other},
		{"wrong part number", "single", "", 7, part.Data},
		{"inline object has no part", "inline", "", 0, part.Data},
		{"missing key", "absent", "", 0, part.Data},
		{"latest is a delete marker", markerKey, "", 0, part.Data},
		{"unknown version id", "single", newVersionID(), 0, part.Data},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := s.UpdateLayout(t.Context(), testBucket, tc.key, tc.versionID, tc.partNum, tc.data, testLayout())
			requireSentinel(t, err, meta.ErrRevisionMismatch)
		})
	}

	got, err := s.GetObject(t.Context(), testBucket, "single", "")
	mustNoErr(t, err)
	requireSamePart(t, part, got.Parts[0])
}

func caseUpdateLayoutMissingBucket(t *testing.T, s meta.Store) {
	err := s.UpdateLayout(t.Context(), "absent", "k", "", 0, ids.DataID{}, testLayout())
	requireCode(t, err, s3err.NoSuchBucket)
}

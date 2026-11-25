package metatest

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// tagsCases covers UpdateTags, which replaces the tag set of one version and
// nothing else.
func tagsCases() []storeCase {
	return []storeCase{
		{"Tags/UpdateLatestKeepsVersion", caseUpdateTagsLatest},
		{"Tags/UpdateNamedVersion", caseUpdateTagsVersion},
		{"Tags/UpdateMissingKey", caseUpdateTagsMissingKey},
		{"Tags/UpdateDeleteMarker", caseUpdateTagsDeleteMarker},
		{"Tags/UpdateRejectsInvalidSet", caseUpdateTagsInvalid},
		{"Tags/UpdateMissingBucket", caseUpdateTagsMissingBucket},
		{"Tags/UpdateLongVersionID", caseUpdateTagsLongVersionID},
	}
}

// caseUpdateTagsLatest checks that an update of the latest version changes its
// tags and nothing else, and that it creates no version: the latest is still
// the same version afterwards.
func caseUpdateTagsLatest(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	v1, v2 := newVersionID(), newVersionID()
	mustPutObject(t, s, objectRecord(testBucket, "k", v1, "one"))
	mustPutObject(t, s, objectRecord(testBucket, "k", v2, "two"))
	before, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)

	got, err := s.UpdateTags(t.Context(), testBucket, "k", "", "env=it&team=storage")
	mustNoErr(t, err)
	if got.VersionID != v2 || got.Tags != "env=it&team=storage" {
		t.Fatalf("UpdateTags returned version %q with tags %q", got.VersionID, got.Tags)
	}
	if got.Inline != nil {
		t.Fatal("UpdateTags returned inline content")
	}

	after, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	if after.VersionID != v2 {
		t.Fatalf("latest after UpdateTags = %q, want %q: a version was created", after.VersionID, v2)
	}
	if after.Tags != "env=it&team=storage" {
		t.Fatalf("stored tags = %q", after.Tags)
	}
	if after.ETag != before.ETag || after.Size != before.Size || !after.ModTime.Equal(before.ModTime) {
		t.Fatalf("UpdateTags changed metadata: before %+v, after %+v", before, after)
	}
	if string(after.Inline) != "two" {
		t.Fatalf("UpdateTags changed the data: %q", after.Inline)
	}
	older, err := s.GetObject(t.Context(), testBucket, "k", v1)
	mustNoErr(t, err)
	if older.Tags != "" {
		t.Fatalf("UpdateTags on the latest changed the older version: tags %q", older.Tags)
	}

	_, err = s.UpdateTags(t.Context(), testBucket, "k", "", "")
	mustNoErr(t, err)
	cleared, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	if cleared.Tags != "" {
		t.Fatalf("empty tag set did not clear the tags: %q", cleared.Tags)
	}
}

// caseUpdateTagsVersion checks that an update names one version, and that the
// other versions and the latest pointer are left alone.
func caseUpdateTagsVersion(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	v1, v2 := newVersionID(), newVersionID()
	mustPutObject(t, s, objectRecord(testBucket, "k", v1, "one"))
	mustPutObject(t, s, objectRecord(testBucket, "k", v2, "two"))

	got, err := s.UpdateTags(t.Context(), testBucket, "k", v1, "team=storage")
	mustNoErr(t, err)
	if got.VersionID != v1 {
		t.Fatalf("UpdateTags returned version %q, want %q", got.VersionID, v1)
	}
	v1Rec, err := s.GetObject(t.Context(), testBucket, "k", v1)
	mustNoErr(t, err)
	if v1Rec.Tags != "team=storage" {
		t.Fatalf("version %q tags = %q", v1, v1Rec.Tags)
	}
	v2Rec, err := s.GetObject(t.Context(), testBucket, "k", v2)
	mustNoErr(t, err)
	if v2Rec.Tags != "" {
		t.Fatalf("version %q tags changed by an update to %q: %q", v2, v1, v2Rec.Tags)
	}
	latest, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	if latest.VersionID != v2 {
		t.Fatalf("latest = %q after an update to an older version, want %q", latest.VersionID, v2)
	}
}

// caseUpdateTagsMissingKey checks the errors for keys and versions that do not
// exist, and that a failed update changes nothing.
func caseUpdateTagsMissingKey(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	mustPutObject(t, s, objectRecord(testBucket, "k", newVersionID(), "one"))

	_, err := s.UpdateTags(t.Context(), testBucket, "absent", "", "a=b")
	requireCode(t, err, s3err.NoSuchKey)
	_, err = s.UpdateTags(t.Context(), testBucket, "absent", newVersionID(), "a=b")
	requireCode(t, err, s3err.NoSuchVersion)
	_, err = s.UpdateTags(t.Context(), testBucket, "k", newVersionID(), "a=b")
	requireCode(t, err, s3err.NoSuchVersion)
}

// caseUpdateTagsDeleteMarker checks that a delete marker is never tagged: a
// request for the latest version reads as an absent key, and a request that
// names the marker is refused. Other versions of the key stay taggable.
func caseUpdateTagsDeleteMarker(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	data := newVersionID()
	mustPutObject(t, s, objectRecord(testBucket, "k", data, "one"))
	res, _, err := s.DeleteObject(t.Context(), testBucket, "k", "", object.Conditions{})
	mustNoErr(t, err)
	marker := res.DeleteMarkerVersionID
	if marker == "" {
		t.Fatal("DeleteObject did not add a delete marker")
	}

	_, err = s.UpdateTags(t.Context(), testBucket, "k", "", "a=b")
	requireCode(t, err, s3err.NoSuchKey)
	_, err = s.UpdateTags(t.Context(), testBucket, "k", marker, "a=b")
	requireCode(t, err, s3err.MethodNotAllowed)
	if _, err := s.UpdateTags(t.Context(), testBucket, "k", data, "a=b"); err != nil {
		t.Fatalf("data version refused after a delete marker was added: %v", err)
	}
	mark, err := s.GetObject(t.Context(), testBucket, "k", marker)
	mustNoErr(t, err)
	if !mark.DeleteMarker || mark.Tags != "" {
		t.Fatalf("delete marker changed: %+v", mark)
	}
}

// caseUpdateTagsInvalid checks that every stored form that breaks the tag rules
// is refused, and that a refused update leaves the tags as they were.
func caseUpdateTagsInvalid(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	mustPutObject(t, s, objectRecord(testBucket, "k", newVersionID(), "one"))
	_, err := s.UpdateTags(t.Context(), testBucket, "k", "", "keep=me")
	mustNoErr(t, err)

	invalid := []string{
		"a=1&a=2",
		"aws:created-by=me",
		"k=%ZZ",
		"a=1&&b=2",
		"=v",
	}
	for _, tags := range invalid {
		if _, err := s.UpdateTags(t.Context(), testBucket, "k", "", tags); err == nil {
			t.Fatalf("UpdateTags accepted %q", tags)
		}
	}
	got, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	if got.Tags != "keep=me" {
		t.Fatalf("refused updates changed the tags: %q", got.Tags)
	}
}

// caseUpdateTagsMissingBucket checks that an update in a missing bucket fails
// with NoSuchBucket.
func caseUpdateTagsMissingBucket(t *testing.T, s meta.Store) {
	_, err := s.UpdateTags(t.Context(), "absent-bucket", "k", "", "a=b")
	requireCode(t, err, s3err.NoSuchBucket)
}

// caseUpdateTagsLongVersionID checks that a version id too long to be stored
// names no version: the store answers NoSuchVersion, the client's error, and
// does not fail with an internal error.
func caseUpdateTagsLongVersionID(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	mustPutObject(t, s, objectRecord(testBucket, "k", newVersionID(), "one"))
	_, err := s.UpdateTags(t.Context(), testBucket, "k", strings.Repeat("v", 65), "a=b")
	requireCode(t, err, s3err.NoSuchVersion)
}

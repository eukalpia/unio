package metatest

import (
	"fmt"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// objectCases covers reads, writes, deletes and conditional requests.
func objectCases() []storeCase {
	return []storeCase{
		{"Object/RoundTrip", caseObjectRoundTrip},
		{"Object/OverwriteReturnsGarbage", caseOverwriteReturnsGarbage},
		{"Object/UnversionedRejectsVersionID", caseUnversionedRejectsVersionID},
		{"Object/SuspendedReplacesNullVersion", caseSuspendedReplacesNullVersion},
		{"Object/EnabledAddsVersions", caseEnabledAddsVersions},
		{"Object/EnabledVersionIDRules", caseEnabledVersionIDRules},
		{"Object/GetMissingKeyAndVersion", caseGetMissingKeyAndVersion},
		{"Object/GetReturnsCopies", caseGetReturnsCopies},
		{"Object/ConditionalCreate", caseConditionalCreate},
		{"Object/ConditionalCreateIsAtomic", caseConditionalCreateIsAtomic},
		{"Object/IfMatch", caseIfMatch},
		{"Object/IfMatchMissingKey", caseIfMatchMissingKey},
		{"Object/CreateAfterDeleteMarker", caseCreateAfterDeleteMarker},
		{"Object/DeleteUnversioned", caseDeleteUnversioned},
		{"Object/DeleteSuspended", caseDeleteSuspended},
		{"Object/DeleteEnabled", caseDeleteEnabled},
		{"Object/DeleteAbsentVersion", caseDeleteAbsentVersion},
		{"Object/DeleteMissingKeyEnabled", caseDeleteMissingKeyEnabled},
		{"Object/DeleteConditional", caseDeleteConditional},
	}
}

func caseObjectRoundTrip(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	want := objectRecord(testBucket, "docs/readme.txt", "", "hello")
	requireCount(t, "garbage of first put", mustPutObject(t, s, want), 0)

	got, err := s.GetObject(t.Context(), testBucket, "docs/readme.txt", "")
	mustNoErr(t, err)
	requireSameRecord(t, want, got)
	if got.DeleteMarker {
		t.Fatal("a data version was read back as a delete marker")
	}
}

func caseOverwriteReturnsGarbage(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustPutObject(t, s, objectRecord(testBucket, "k", "", "one"))

	garbage := mustPutObject(t, s, objectRecord(testBucket, "k", "", "two"))
	requireCount(t, "garbage of overwrite", garbage, 1)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "one"), garbage[0])

	got, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "two"), got)
}

func caseUnversionedRejectsVersionID(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	_, err := s.PutObject(t.Context(), objectRecord(testBucket, "k", newVersionID(), "x"), object.Conditions{})
	requireCode(t, err, s3err.InvalidArgument)
}

func caseSuspendedReplacesNullVersion(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningSuspended)
	requireCount(t, "first suspended put", mustPutObject(t, s, objectRecord(testBucket, "k", "", "one")), 0)

	garbage := mustPutObject(t, s, objectRecord(testBucket, "k", "", "two"))
	requireCount(t, "garbage of suspended overwrite", garbage, 1)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "one"), garbage[0])

	_, err := s.PutObject(t.Context(), objectRecord(testBucket, "k", newVersionID(), "x"), object.Conditions{})
	requireCode(t, err, s3err.InvalidArgument)
}

func caseEnabledAddsVersions(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	idA, idB := newVersionID(), newVersionID()
	requireCount(t, "garbage of first version", mustPutObject(t, s, objectRecord(testBucket, "k", idA, "one")), 0)
	requireCount(t, "garbage of second version", mustPutObject(t, s, objectRecord(testBucket, "k", idB, "two")), 0)

	latest, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireSameRecord(t, objectRecord(testBucket, "k", idB, "two"), latest)

	first, err := s.GetObject(t.Context(), testBucket, "k", idA)
	mustNoErr(t, err)
	requireSameRecord(t, objectRecord(testBucket, "k", idA, "one"), first)

	_, err = s.GetObject(t.Context(), testBucket, "k", newVersionID())
	requireCode(t, err, s3err.NoSuchVersion)
}

func caseEnabledVersionIDRules(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	id := newVersionID()
	mustPutObject(t, s, objectRecord(testBucket, "k", id, "one"))

	_, err := s.PutObject(t.Context(), objectRecord(testBucket, "k", "", "x"), object.Conditions{})
	requireCode(t, err, s3err.InvalidArgument)

	_, err = s.PutObject(t.Context(), objectRecord(testBucket, "k", id, "again"), object.Conditions{})
	requireCode(t, err, s3err.InvalidArgument)
	kept, err := s.GetObject(t.Context(), testBucket, "k", id)
	mustNoErr(t, err)
	requireSameRecord(t, objectRecord(testBucket, "k", id, "one"), kept)

	// Version ids are unique per key, so another key may reuse one.
	_, err = s.PutObject(t.Context(), objectRecord(testBucket, "other", id, "two"), object.Conditions{})
	mustNoErr(t, err)
}

func caseGetMissingKeyAndVersion(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	_, err := s.GetObject(t.Context(), testBucket, "absent", "")
	requireCode(t, err, s3err.NoSuchKey)
	_, err = s.GetObject(t.Context(), testBucket, "absent", newVersionID())
	requireCode(t, err, s3err.NoSuchVersion)
}

func caseGetReturnsCopies(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	rec := objectRecord(testBucket, "k", "", "payload")
	mustPutObject(t, s, rec)
	// Mutating the input after the write must not reach the stored record.
	rec.Inline[0] = 'X'
	rec.UserMetadata["suite"] = "changed"

	want := objectRecord(testBucket, "k", "", "payload")
	got, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireSameRecord(t, want, got)
	// Mutating a read result must not reach the stored record either.
	got.Inline[0] = 'Y'
	got.UserMetadata["suite"] = "changed-again"

	again, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireSameRecord(t, want, again)
}

func caseConditionalCreate(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	none := object.Conditions{IfNoneMatch: "*"}
	_, err := s.PutObject(t.Context(), objectRecord(testBucket, "k", "", "one"), none)
	mustNoErr(t, err)

	_, err = s.PutObject(t.Context(), objectRecord(testBucket, "k", "", "two"), none)
	requireCode(t, err, s3err.PreconditionFailed)
	got, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "one"), got)
}

// caseConditionalCreateIsAtomic races writers that all require the key to be
// absent. Exactly one may win.
func caseConditionalCreateIsAtomic(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	const writers = 8
	start := make(chan struct{})
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := objectRecord(testBucket, "race", "", fmt.Sprintf("writer %d", i))
			_, err := s.PutObject(t.Context(), rec, object.Conditions{IfNoneMatch: "*"})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case s3err.CodeOf(err) == s3err.PreconditionFailed:
		default:
			t.Errorf("losing writer got %v, want PreconditionFailed", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d writers succeeded with If-None-Match: *, want exactly 1", won)
	}
}

func caseIfMatch(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustPutObject(t, s, objectRecord(testBucket, "k", "", "one"))

	_, err := s.PutObject(t.Context(), objectRecord(testBucket, "k", "", "two"), object.Conditions{IfMatch: "wrong"})
	requireCode(t, err, s3err.PreconditionFailed)
	got, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "one"), got)

	garbage, err := s.PutObject(t.Context(), objectRecord(testBucket, "k", "", "two"), object.Conditions{IfMatch: etagOf("one")})
	mustNoErr(t, err)
	requireCount(t, "garbage of matched overwrite", garbage, 1)

	garbage, err = s.PutObject(t.Context(), objectRecord(testBucket, "k", "", "three"), object.Conditions{IfMatch: "*"})
	mustNoErr(t, err)
	requireCount(t, "garbage of If-Match: * overwrite", garbage, 1)
}

func caseIfMatchMissingKey(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	for _, ifMatch := range []string{"abc", "*"} {
		_, err := s.PutObject(t.Context(), objectRecord(testBucket, "absent", "", "x"), object.Conditions{IfMatch: ifMatch})
		requireCode(t, err, s3err.NoSuchKey)
	}
	_, err := s.GetObject(t.Context(), testBucket, "absent", "")
	requireCode(t, err, s3err.NoSuchKey)
}

// caseCreateAfterDeleteMarker checks that a delete marker counts as a missing
// object for write preconditions.
func caseCreateAfterDeleteMarker(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	mustPutObject(t, s, objectRecord(testBucket, "k", newVersionID(), "one"))
	_, _, err := s.DeleteObject(t.Context(), testBucket, "k", "", object.Conditions{})
	mustNoErr(t, err)

	_, err = s.PutObject(t.Context(), objectRecord(testBucket, "k", newVersionID(), "two"), object.Conditions{IfNoneMatch: "*"})
	mustNoErr(t, err)
}

func caseDeleteUnversioned(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustPutObject(t, s, objectRecord(testBucket, "k", "", "data"))

	res, garbage, err := s.DeleteObject(t.Context(), testBucket, "k", "", object.Conditions{})
	mustNoErr(t, err)
	if res.Key != "k" || res.DeleteMarker || res.DeleteMarkerVersionID != "" {
		t.Fatalf("DeleteResult = %+v, want key k and no marker", res)
	}
	requireCount(t, "garbage of unversioned delete", garbage, 1)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "data"), garbage[0])
	_, err = s.GetObject(t.Context(), testBucket, "k", "")
	requireCode(t, err, s3err.NoSuchKey)

	res, garbage, err = s.DeleteObject(t.Context(), testBucket, "absent", "", object.Conditions{})
	mustNoErr(t, err)
	if res.Key != "absent" || res.DeleteMarker {
		t.Fatalf("DeleteResult for missing key = %+v", res)
	}
	requireCount(t, "garbage of missing-key delete", garbage, 0)
}

func caseDeleteSuspended(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningSuspended)
	mustPutObject(t, s, objectRecord(testBucket, "k", "", "data"))

	res, garbage, err := s.DeleteObject(t.Context(), testBucket, "k", "", object.Conditions{})
	mustNoErr(t, err)
	if !res.DeleteMarker || res.DeleteMarkerVersionID != "" {
		t.Fatalf("DeleteResult = %+v, want a null delete marker", res)
	}
	requireCount(t, "garbage of suspended delete", garbage, 1)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "data"), garbage[0])

	marker, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	if !marker.DeleteMarker || marker.VersionID != "" {
		t.Fatalf("latest after suspended delete = %+v, want the null delete marker", marker)
	}

	// A new write replaces the null delete marker.
	garbage = mustPutObject(t, s, objectRecord(testBucket, "k", "", "again"))
	requireCount(t, "garbage of write over marker", garbage, 1)
	if !garbage[0].DeleteMarker {
		t.Fatal("the replaced null version should be the delete marker")
	}
	got, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "again"), got)
}

func caseDeleteEnabled(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	idA, idB := newVersionID(), newVersionID()
	mustPutObject(t, s, objectRecord(testBucket, "k", idA, "one"))
	mustPutObject(t, s, objectRecord(testBucket, "k", idB, "two"))

	res, garbage, err := s.DeleteObject(t.Context(), testBucket, "k", "", object.Conditions{})
	mustNoErr(t, err)
	marker := res.DeleteMarkerVersionID
	if !res.DeleteMarker || marker == "" || marker == idA || marker == idB {
		t.Fatalf("DeleteResult = %+v, want a new delete marker", res)
	}
	if res.Key != "k" || res.VersionID != marker {
		t.Fatalf("DeleteResult = %+v, want key k and version id %q", res, marker)
	}
	requireCount(t, "garbage of marker-adding delete", garbage, 0)

	latest, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	if !latest.DeleteMarker || latest.VersionID != marker {
		t.Fatalf("latest = %+v, want delete marker %q", latest, marker)
	}
	gotMarker, err := s.GetObject(t.Context(), testBucket, "k", marker)
	mustNoErr(t, err)
	if !gotMarker.DeleteMarker {
		t.Fatal("GetObject by marker id did not return a delete marker")
	}
	requireSameRecord(t, objectRecord(testBucket, "k", idA, "one"), mustGetVersion(t, s, "k", idA))

	// Deleting a version by id removes that version only, with its data as garbage.
	res, garbage, err = s.DeleteObject(t.Context(), testBucket, "k", idA, object.Conditions{})
	mustNoErr(t, err)
	if res.DeleteMarker || res.VersionID != idA {
		t.Fatalf("DeleteResult for data version = %+v", res)
	}
	requireCount(t, "garbage of version delete", garbage, 1)
	requireSameRecord(t, objectRecord(testBucket, "k", idA, "one"), garbage[0])
	_, err = s.GetObject(t.Context(), testBucket, "k", idA)
	requireCode(t, err, s3err.NoSuchVersion)

	// Deleting the marker by id reveals the previous latest version.
	res, _, err = s.DeleteObject(t.Context(), testBucket, "k", marker, object.Conditions{})
	mustNoErr(t, err)
	if !res.DeleteMarker || res.DeleteMarkerVersionID != marker {
		t.Fatalf("DeleteResult for marker = %+v", res)
	}
	revealed, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireSameRecord(t, objectRecord(testBucket, "k", idB, "two"), revealed)
}

// mustGetVersion returns the version of key with id versionID.
func mustGetVersion(t *testing.T, s meta.Store, key, versionID string) meta.ObjectRecord {
	t.Helper()
	rec, err := s.GetObject(t.Context(), testBucket, key, versionID)
	mustNoErr(t, err)
	return rec
}

func caseDeleteAbsentVersion(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	id := newVersionID()
	mustPutObject(t, s, objectRecord(testBucket, "k", id, "one"))

	res, garbage, err := s.DeleteObject(t.Context(), testBucket, "k", newVersionID(), object.Conditions{})
	mustNoErr(t, err)
	if res.DeleteMarker {
		t.Fatalf("deleting an absent version reported a marker: %+v", res)
	}
	requireCount(t, "garbage of absent-version delete", garbage, 0)
	requireSameRecord(t, objectRecord(testBucket, "k", id, "one"), mustGetVersion(t, s, "k", id))
}

func caseDeleteMissingKeyEnabled(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	res, _, err := s.DeleteObject(t.Context(), testBucket, "absent", "", object.Conditions{})
	mustNoErr(t, err)
	if !res.DeleteMarker || res.DeleteMarkerVersionID == "" {
		t.Fatalf("DeleteResult for missing key in a versioned bucket = %+v, want a marker", res)
	}
	latest, err := s.GetObject(t.Context(), testBucket, "absent", "")
	mustNoErr(t, err)
	if !latest.DeleteMarker {
		t.Fatal("latest version of the missing key should be the delete marker")
	}
}

func caseDeleteConditional(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustPutObject(t, s, objectRecord(testBucket, "k", "", "data"))

	_, _, err := s.DeleteObject(t.Context(), testBucket, "k", "", object.Conditions{IfMatch: "wrong"})
	requireCode(t, err, s3err.PreconditionFailed)
	_, err = s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)

	_, garbage, err := s.DeleteObject(t.Context(), testBucket, "k", "", object.Conditions{IfMatch: etagOf("data")})
	mustNoErr(t, err)
	requireCount(t, "garbage of matched delete", garbage, 1)
}

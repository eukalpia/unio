package metatest

import (
	"fmt"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// uploadCases covers multipart uploads: parts, completion, abort and the
// listing of in-progress uploads.
func uploadCases() []storeCase {
	return []storeCase{
		{"Upload/CreateAndGet", caseUploadCreateAndGet},
		{"Upload/CreateDuplicateID", caseUploadCreateDuplicateID},
		{"Upload/GetMissingAndWrongKey", caseUploadGetMissing},
		{"Upload/PutPartReplaces", caseUploadPutPartReplaces},
		{"Upload/PutPartMissingUpload", caseUploadPutPartMissing},
		{"Upload/ListPartsPaging", caseUploadListPartsPaging},
		{"Upload/ListPartsErrors", caseUploadListPartsErrors},
		{"Upload/CompleteBadOrder", caseUploadCompleteBadOrder},
		{"Upload/CompleteMissingPart", caseUploadCompleteMissingPart},
		{"Upload/CompleteMismatch", caseUploadCompleteMismatch},
		{"Upload/CompleteUnusedAndStoredParts", caseUploadCompleteUnused},
		{"Upload/CompleteReplacesNullVersion", caseUploadCompleteReplacesNull},
		{"Upload/CompleteEnabledVersionRules", caseUploadCompleteEnabled},
		{"Upload/CompleteConditionFails", caseUploadCompleteConditions},
		{"Upload/CompleteMissingUpload", caseUploadCompleteMissingUpload},
		{"Upload/AbortReturnsParts", caseUploadAbort},
		{"Upload/ListUploadsPaging", caseListUploadsPaging},
		{"Upload/ListUploadsDelimiter", caseListUploadsDelimiter},
		{"Upload/ListUploadsInvalidMax", caseListUploadsInvalidMax},
	}
}

// mustPutPart stores part in an upload and returns the replaced part, if any.
func mustPutPart(t *testing.T, s meta.Store, key, uploadID string, part meta.PartRecord) *meta.PartRecord {
	t.Helper()
	garbage, err := s.PutPart(t.Context(), testBucket, key, uploadID, part)
	mustNoErr(t, err)
	return garbage
}

// mustCreateUpload starts an upload of key with id uploadID in testBucket.
func mustCreateUpload(t *testing.T, s meta.Store, key, uploadID string) {
	t.Helper()
	mustNoErr(t, s.CreateUpload(t.Context(), uploadRecord(testBucket, key, uploadID)))
}

// completionRecord returns the object a CompleteUpload call commits, with the
// given parts. The parts are the caller's copies, not the stored ones.
func completionRecord(bucket, key, versionID string, parts ...meta.PartRecord) meta.ObjectRecord {
	return meta.ObjectRecord{
		Bucket:    bucket,
		Key:       key,
		VersionID: versionID,
		ModTime:   testTime(),
		Multipart: true,
		ETag:      fmt.Sprintf("%s-%d", etagOf(key), len(parts)),
		Parts:     parts,
	}
}

// callerCopy returns the fields a caller sends in CompleteUpload for a stored
// part: its number, ETag and data id, without the stored size and layout.
func callerCopy(p meta.PartRecord) meta.PartRecord {
	return meta.PartRecord{Number: p.Number, ETag: p.ETag, Data: p.Data}
}

// requireSamePart fails the test unless two part records are equal.
func requireSamePart(t *testing.T, want, got meta.PartRecord) {
	t.Helper()
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("part mismatch (-want +got):\n%s", diff)
	}
}

func caseUploadCreateAndGet(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	up := uploadRecord(testBucket, "big.bin", "upload-1")
	mustNoErr(t, s.CreateUpload(t.Context(), up))
	got, err := s.GetUpload(t.Context(), testBucket, "big.bin", "upload-1")
	mustNoErr(t, err)
	requireSameUpload(t, up, got)
}

func caseUploadCreateDuplicateID(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "a", "dup")
	requireCode(t, s.CreateUpload(t.Context(), uploadRecord(testBucket, "a", "dup")), s3err.InvalidArgument)
}

func caseUploadGetMissing(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "key", "upload-1")
	_, err := s.GetUpload(t.Context(), testBucket, "key", "nope")
	requireCode(t, err, s3err.NoSuchUpload)
	_, err = s.GetUpload(t.Context(), testBucket, "other-key", "upload-1")
	requireCode(t, err, s3err.NoSuchUpload)
}

func caseUploadPutPartReplaces(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "k", "u")
	first := partRecord(1, "first")
	if garbage := mustPutPart(t, s, "k", "u", first); garbage != nil {
		t.Fatalf("first PutPart returned garbage %+v", garbage)
	}
	second := partRecord(1, "second")
	garbage := mustPutPart(t, s, "k", "u", second)
	if garbage == nil {
		t.Fatal("replacing a part returned no garbage")
	}
	requireSamePart(t, first, *garbage)

	parts, _, err := s.ListParts(t.Context(), testBucket, "k", "u", object.ListPartsOptions{})
	mustNoErr(t, err)
	requireNumbers(t, "after replace", parts, 1)
	requireSamePart(t, second, parts[0])
}

func caseUploadPutPartMissing(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "key", "u")
	_, err := s.PutPart(t.Context(), testBucket, "key", "nope", partRecord(1, "p"))
	requireCode(t, err, s3err.NoSuchUpload)
	_, err = s.PutPart(t.Context(), testBucket, "other", "u", partRecord(1, "p"))
	requireCode(t, err, s3err.NoSuchUpload)
}

func caseUploadListPartsPaging(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "k", "u")
	for n := 1; n <= 5; n++ {
		mustPutPart(t, s, "k", "u", partRecord(n, fmt.Sprintf("part %d", n)))
	}

	tests := []struct {
		name    string
		opts    object.ListPartsOptions
		want    []int
		trunc   bool
		nextNum int
	}{
		{"first page", object.ListPartsOptions{MaxParts: 2}, []int{1, 2}, true, 2},
		{"second page", object.ListPartsOptions{PartNumberMarker: 2, MaxParts: 2}, []int{3, 4}, true, 4},
		{"last page", object.ListPartsOptions{PartNumberMarker: 4, MaxParts: 2}, []int{5}, false, 5},
		{"default page", object.ListPartsOptions{}, []int{1, 2, 3, 4, 5}, false, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts, res, err := s.ListParts(t.Context(), testBucket, "k", "u", tc.opts)
			mustNoErr(t, err)
			requireNumbers(t, "page", parts, tc.want...)
			if res.IsTruncated != tc.trunc {
				t.Fatalf("IsTruncated = %v, want %v", res.IsTruncated, tc.trunc)
			}
			if res.NextPartNumberMarker != tc.nextNum {
				t.Fatalf("NextPartNumberMarker = %d, want %d", res.NextPartNumberMarker, tc.nextNum)
			}
			if res.ChecksumAlgorithm != object.ChecksumCRC32C {
				t.Fatalf("ChecksumAlgorithm = %v, want the upload's CRC32C", res.ChecksumAlgorithm)
			}
		})
	}
}

func caseUploadListPartsErrors(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "k", "u")
	_, _, err := s.ListParts(t.Context(), testBucket, "k", "u", object.ListPartsOptions{MaxParts: 1001})
	requireCode(t, err, s3err.InvalidArgument)
	_, _, err = s.ListParts(t.Context(), testBucket, "k", "nope", object.ListPartsOptions{})
	requireCode(t, err, s3err.NoSuchUpload)
}

func caseUploadCompleteBadOrder(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "k", "u")
	p1 := partRecord(1, "one")
	p2 := partRecord(2, "two")
	mustPutPart(t, s, "k", "u", p1)
	mustPutPart(t, s, "k", "u", p2)

	for _, parts := range [][]meta.PartRecord{{p2, p1}, {p1, p1}} {
		_, _, err := s.CompleteUpload(t.Context(), "u", completionRecord(testBucket, "k", "", parts...), object.Conditions{})
		requireCode(t, err, s3err.InvalidPartOrder)
	}
	_, err := s.GetUpload(t.Context(), testBucket, "k", "u")
	mustNoErr(t, err)
}

func caseUploadCompleteMissingPart(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "k", "u")
	p1 := partRecord(1, "one")
	mustPutPart(t, s, "k", "u", p1)
	_, _, err := s.CompleteUpload(t.Context(), "u", completionRecord(testBucket, "k", "", p1, partRecord(9, "never sent")), object.Conditions{})
	requireCode(t, err, s3err.InvalidPart)
}

// caseUploadCompleteMismatch checks that a part whose ETag or data id differs
// from the stored part fails the completion and leaves the upload in place.
func caseUploadCompleteMismatch(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "k", "u")
	p1 := partRecord(1, "one")
	mustPutPart(t, s, "k", "u", p1)

	wrongETag := callerCopy(p1)
	wrongETag.ETag = etagOf("something else")
	wrongData := callerCopy(p1)
	wrongData.Data = ids.NewDataID()
	cases := []struct {
		name string
		part meta.PartRecord
	}{{"etag", wrongETag}, {"data id", wrongData}}
	for _, tc := range cases {
		part := tc.part
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := s.CompleteUpload(t.Context(), "u", completionRecord(testBucket, "k", "", part), object.Conditions{})
			requireCode(t, err, s3err.InvalidPart)
		})
	}
	_, err := s.GetUpload(t.Context(), testBucket, "k", "u")
	mustNoErr(t, err)
	_, err = s.GetObject(t.Context(), testBucket, "k", "")
	requireCode(t, err, s3err.NoSuchKey)
}

// caseUploadCompleteUnused checks that completion uses the stored parts,
// returns the parts it does not use, and removes the upload.
func caseUploadCompleteUnused(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "k", "u")
	p1 := partRecord(1, "one")
	p2 := partRecord(2, "two")
	p3 := partRecord(3, "three")
	for _, p := range []meta.PartRecord{p1, p2, p3} {
		mustPutPart(t, s, "k", "u", p)
	}

	garbage, unused, err := s.CompleteUpload(t.Context(), "u",
		completionRecord(testBucket, "k", "", callerCopy(p1), callerCopy(p3)), object.Conditions{})
	mustNoErr(t, err)
	requireCount(t, "garbage", garbage, 0)
	requireNumbers(t, "unused", unused, 2)
	requireSamePart(t, p2, unused[0])

	obj, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireNumbers(t, "object parts", obj.Parts, 1, 3)
	requireSamePart(t, p1, obj.Parts[0])
	requireSamePart(t, p3, obj.Parts[1])

	_, err = s.GetUpload(t.Context(), testBucket, "k", "u")
	requireCode(t, err, s3err.NoSuchUpload)
	_, _, err = s.CompleteUpload(t.Context(), "u", completionRecord(testBucket, "k", "", callerCopy(p1)), object.Conditions{})
	requireCode(t, err, s3err.NoSuchUpload)
}

func caseUploadCompleteReplacesNull(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustPutObject(t, s, objectRecord(testBucket, "k", "", "old"))
	mustCreateUpload(t, s, "k", "u")
	p1 := partRecord(1, "new")
	mustPutPart(t, s, "k", "u", p1)

	rec := completionRecord(testBucket, "k", "", p1)
	garbage, _, err := s.CompleteUpload(t.Context(), "u", rec, object.Conditions{})
	mustNoErr(t, err)
	requireCount(t, "garbage", garbage, 1)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "old"), garbage[0])
	got, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	if got.ETag != rec.ETag || !got.Multipart {
		t.Fatalf("stored object = %+v, want the completed multipart object", got)
	}
}

func caseUploadCompleteEnabled(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	mustCreateUpload(t, s, "k", "u1")
	p1 := partRecord(1, "one")
	mustPutPart(t, s, "k", "u1", p1)

	_, _, err := s.CompleteUpload(t.Context(), "u1", completionRecord(testBucket, "k", "", p1), object.Conditions{})
	requireCode(t, err, s3err.InvalidArgument)
	_, err = s.GetUpload(t.Context(), testBucket, "k", "u1")
	mustNoErr(t, err)

	id := newVersionID()
	garbage, unused, err := s.CompleteUpload(t.Context(), "u1", completionRecord(testBucket, "k", id, p1), object.Conditions{})
	mustNoErr(t, err)
	requireCount(t, "garbage in versioned bucket", garbage, 0)
	requireCount(t, "unused parts", unused, 0)
	obj, err := s.GetObject(t.Context(), testBucket, "k", id)
	mustNoErr(t, err)
	requireNumbers(t, "object parts", obj.Parts, 1)

	// A second upload that reuses the version id must fail.
	mustCreateUpload(t, s, "k", "u2")
	mustPutPart(t, s, "k", "u2", partRecord(1, "two"))
	_, _, err = s.CompleteUpload(t.Context(), "u2", completionRecord(testBucket, "k", id), object.Conditions{})
	requireCode(t, err, s3err.InvalidArgument)
	_, err = s.GetUpload(t.Context(), testBucket, "k", "u2")
	mustNoErr(t, err)
}

func caseUploadCompleteConditions(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustPutObject(t, s, objectRecord(testBucket, "k", "", "old"))
	mustCreateUpload(t, s, "k", "u")
	p1 := partRecord(1, "new")
	mustPutPart(t, s, "k", "u", p1)

	rec := completionRecord(testBucket, "k", "", p1)
	_, _, err := s.CompleteUpload(t.Context(), "u", rec, object.Conditions{IfNoneMatch: "*"})
	requireCode(t, err, s3err.PreconditionFailed)
	_, _, err = s.CompleteUpload(t.Context(), "u", rec, object.Conditions{IfMatch: "wrong"})
	requireCode(t, err, s3err.PreconditionFailed)
	_, err = s.GetUpload(t.Context(), testBucket, "k", "u")
	mustNoErr(t, err)
	got, err := s.GetObject(t.Context(), testBucket, "k", "")
	mustNoErr(t, err)
	requireSameRecord(t, objectRecord(testBucket, "k", "", "old"), got)

	garbage, _, err := s.CompleteUpload(t.Context(), "u", rec, object.Conditions{IfMatch: etagOf("old")})
	mustNoErr(t, err)
	requireCount(t, "garbage of matched completion", garbage, 1)
}

func caseUploadCompleteMissingUpload(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "k", "u")
	_, _, err := s.CompleteUpload(t.Context(), "nope", completionRecord(testBucket, "k", ""), object.Conditions{})
	requireCode(t, err, s3err.NoSuchUpload)
	_, _, err = s.CompleteUpload(t.Context(), "u", completionRecord(testBucket, "other", ""), object.Conditions{})
	requireCode(t, err, s3err.NoSuchUpload)
}

func caseUploadAbort(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "k", "u")
	for n := 1; n <= 3; n++ {
		mustPutPart(t, s, "k", "u", partRecord(n, fmt.Sprintf("part %d", n)))
	}
	parts, err := s.AbortUpload(t.Context(), testBucket, "k", "u")
	mustNoErr(t, err)
	requireNumbers(t, "aborted parts", parts, 1, 2, 3)

	_, err = s.GetUpload(t.Context(), testBucket, "k", "u")
	requireCode(t, err, s3err.NoSuchUpload)
	_, err = s.AbortUpload(t.Context(), testBucket, "k", "u")
	requireCode(t, err, s3err.NoSuchUpload)
}

// uploadItem is one entry of an upload listing. Prefix is true for a common
// prefix, whose name is in Key.
type uploadItem struct {
	Key      string
	UploadID string
	Prefix   bool
}

// sortUploadItems orders items by (Key, UploadID), the listing order.
func sortUploadItems(items []uploadItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Key != items[j].Key {
			return items[i].Key < items[j].Key
		}
		return items[i].UploadID < items[j].UploadID
	})
}

// listAllUploads pages through ListUploads, following the key and upload id
// markers, and returns each page's items in listing order.
func listAllUploads(t *testing.T, s meta.Store, bucket string, opts object.ListUploadsOptions) [][]uploadItem {
	t.Helper()
	var pages [][]uploadItem
	for range maxListPages {
		res, err := s.ListUploads(t.Context(), bucket, opts)
		mustNoErr(t, err)
		var page []uploadItem
		for _, u := range res.Uploads {
			page = append(page, uploadItem{Key: u.Key, UploadID: u.UploadID})
		}
		for _, p := range res.CommonPrefixes {
			page = append(page, uploadItem{Key: p, Prefix: true})
		}
		sortUploadItems(page)
		if opts.MaxUploads > 0 && len(page) > opts.MaxUploads {
			t.Fatalf("page has %d items, MaxUploads is %d", len(page), opts.MaxUploads)
		}
		pages = append(pages, page)
		if !res.IsTruncated {
			return pages
		}
		if len(page) == 0 {
			t.Fatal("truncated upload page is empty")
		}
		last := page[len(page)-1]
		if last.Key != res.NextKeyMarker || last.UploadID != res.NextUploadIDMarker {
			t.Fatalf("markers = (%q, %q), want the last item (%q, %q)",
				res.NextKeyMarker, res.NextUploadIDMarker, last.Key, last.UploadID)
		}
		opts.KeyMarker = res.NextKeyMarker
		opts.UploadIDMarker = res.NextUploadIDMarker
	}
	t.Fatalf("upload listing did not end after %d pages", maxListPages)
	return nil
}

// flattenUploadPages concatenates the items of every page.
func flattenUploadPages(pages [][]uploadItem) []uploadItem {
	var out []uploadItem
	for _, p := range pages {
		out = append(out, p...)
	}
	return out
}

// caseListUploadsPaging lists uploads whose keys share several uploads each,
// so that pages end in the middle of a key.
func caseListUploadsPaging(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	var want []uploadItem
	for ki := range 5 {
		key := fmt.Sprintf("key-%d", ki)
		for n := range 5 {
			id := fmt.Sprintf("u-%d-%d", ki, n)
			mustNoErr(t, s.CreateUpload(t.Context(), uploadRecord(testBucket, key, id)))
			want = append(want, uploadItem{Key: key, UploadID: id})
		}
	}
	sortUploadItems(want)

	pages := listAllUploads(t, s, testBucket, object.ListUploadsOptions{MaxUploads: 3})
	requireCount(t, "pages", pages, 9)
	if diff := cmp.Diff(want, flattenUploadPages(pages)); diff != "" {
		t.Fatalf("uploads mismatch (-want +got):\n%s", diff)
	}
	res, err := s.ListUploads(t.Context(), testBucket, object.ListUploadsOptions{})
	mustNoErr(t, err)
	requireCount(t, "default page", res.Uploads, 25)
}

// caseListUploadsDelimiter checks grouping by delimiter, with and without a
// prefix, and paging over a common prefix.
func caseListUploadsDelimiter(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	mustCreateUpload(t, s, "d/1", "u1")
	mustCreateUpload(t, s, "d/2", "u2")
	mustCreateUpload(t, s, "d/sub/x", "u4")
	mustCreateUpload(t, s, "top", "u3")

	pages := listAllUploads(t, s, testBucket, object.ListUploadsOptions{Delimiter: "/", MaxUploads: 1})
	want := []uploadItem{{Key: "d/", Prefix: true}, {Key: "top", UploadID: "u3"}}
	if diff := cmp.Diff(want, flattenUploadPages(pages)); diff != "" {
		t.Fatalf("delimited uploads mismatch (-want +got):\n%s", diff)
	}

	pages = listAllUploads(t, s, testBucket, object.ListUploadsOptions{Prefix: "d/", Delimiter: "/"})
	want = []uploadItem{{Key: "d/1", UploadID: "u1"}, {Key: "d/2", UploadID: "u2"}, {Key: "d/sub/", Prefix: true}}
	if diff := cmp.Diff(want, flattenUploadPages(pages)); diff != "" {
		t.Fatalf("prefixed uploads mismatch (-want +got):\n%s", diff)
	}
}

func caseListUploadsInvalidMax(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	for _, n := range []int{-1, 1001} {
		_, err := s.ListUploads(t.Context(), testBucket, object.ListUploadsOptions{MaxUploads: n})
		requireCode(t, err, s3err.InvalidArgument)
	}
}

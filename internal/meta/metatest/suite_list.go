package metatest

import (
	"fmt"
	"sort"
	"testing"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// maxListPages bounds listing loops so that a broken continuation fails
// instead of hanging.
const maxListPages = 10000

// listCases covers ListObjects: filters, delimiters, markers, page sizes and
// key encodings.
func listCases() []storeCase {
	return []storeCase{
		{"List/Empty", caseListEmpty},
		{"List/PrefixDelimiterTable", caseListPrefixDelimiterTable},
		{"List/HidesDeleteMarkersAndOldVersions", caseListHidesNonLatest},
		{"List/PagesWithMaxKeys", caseListPagesWithMaxKeys},
		{"List/InvalidMaxKeys", caseListInvalidMaxKeys},
		{"List/UnicodeAndControlKeys", caseListUnicodeAndControlKeys},
		{"List/LargePaging", caseListLargePaging},
	}
}

// listPageItems returns the names on a page, objects and common prefixes
// merged in byte order. It checks that the page is internally sorted and
// that the continuation marker is the last name on it.
func listPageItems(t *testing.T, res meta.ListResult) []string {
	t.Helper()
	names := make([]string, 0, len(res.Objects)+len(res.CommonPrefixes))
	for _, o := range res.Objects {
		names = append(names, o.Key)
	}
	names = append(names, res.CommonPrefixes...)
	sort.Strings(names)
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			t.Fatalf("page repeats %q", names[i])
		}
	}
	if len(names) > 0 && res.NextAfter != names[len(names)-1] {
		t.Fatalf("NextAfter = %q, want last item %q", res.NextAfter, names[len(names)-1])
	}
	return names
}

// listAllPages pages through ListObjects with opts, following NextAfter
// until the listing is not truncated. It returns the items of each page.
func listAllPages(t *testing.T, s meta.Store, bucket string, opts object.ListOptions) [][]string {
	t.Helper()
	var pages [][]string
	for range maxListPages {
		res, err := s.ListObjects(t.Context(), bucket, opts)
		mustNoErr(t, err)
		items := listPageItems(t, res)
		if opts.MaxKeys > 0 && len(items) > opts.MaxKeys {
			t.Fatalf("page has %d items, MaxKeys is %d", len(items), opts.MaxKeys)
		}
		pages = append(pages, items)
		if !res.IsTruncated {
			return pages
		}
		if len(items) == 0 {
			t.Fatal("truncated page is empty")
		}
		opts.After = res.NextAfter
	}
	t.Fatalf("listing did not end after %d pages", maxListPages)
	return nil
}

// flattenPages concatenates the items of every page.
func flattenPages(pages [][]string) []string {
	var out []string
	for _, p := range pages {
		out = append(out, p...)
	}
	return out
}

// sortedCopy returns a sorted copy of names.
func sortedCopy(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// populateBucket writes one unversioned object for each key.
func populateBucket(t *testing.T, s meta.Store, bucket string, keys []string) {
	t.Helper()
	for _, k := range keys {
		mustPutObject(t, s, objectRecord(bucket, k, "", k))
	}
}

func caseListEmpty(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	pages := listAllPages(t, s, testBucket, object.ListOptions{})
	requireCount(t, "pages of empty listing", pages, 1)
	requireCount(t, "items of empty listing", pages[0], 0)
}

// caseListPrefixDelimiterTable checks prefix, delimiter and After handling
// against one fixed key set.
func caseListPrefixDelimiterTable(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	keys := []string{"a/1", "a/2", "a/x/1", "a/x/2", "b/1", "c", "ab"}
	populateBucket(t, s, testBucket, keys)

	const slash = "/"
	tests := []struct {
		name     string
		opts     object.ListOptions
		objects  []string
		prefixes []string
	}{
		{
			name:    "prefix without delimiter",
			opts:    object.ListOptions{Prefix: "a/"},
			objects: []string{"a/1", "a/2", "a/x/1", "a/x/2"},
		},
		{
			name:     "prefix with delimiter",
			opts:     object.ListOptions{Prefix: "a/", Delimiter: slash},
			objects:  []string{"a/1", "a/2"},
			prefixes: []string{"a/x/"},
		},
		{
			name:     "root delimiter",
			opts:     object.ListOptions{Delimiter: slash},
			objects:  []string{"ab", "c"},
			prefixes: []string{"a/", "b/"},
		},
		{
			name:     "partial prefix",
			opts:     object.ListOptions{Prefix: "a", Delimiter: slash},
			objects:  []string{"ab"},
			prefixes: []string{"a/"},
		},
		{
			name:    "prefix at a delimiter boundary",
			opts:    object.ListOptions{Prefix: "a/x/", Delimiter: slash},
			objects: []string{"a/x/1", "a/x/2"},
		},
		{
			name:    "no filters",
			opts:    object.ListOptions{},
			objects: []string{"a/1", "a/2", "a/x/1", "a/x/2", "ab", "b/1", "c"},
		},
		{
			name:     "after a key, delimiter rolls up later keys",
			opts:     object.ListOptions{After: "a/2", Delimiter: slash},
			objects:  []string{"ab", "c"},
			prefixes: []string{"b/"},
		},
		{
			name:    "after the last prefix key",
			opts:    object.ListOptions{After: "b/1"},
			objects: []string{"c"},
		},
		{
			name:     "after a common prefix skips it",
			opts:     object.ListOptions{After: "a/", Delimiter: slash},
			objects:  []string{"ab", "c"},
			prefixes: []string{"b/"},
		},
		{
			name:     "after a key inside a common prefix skips that prefix",
			opts:     object.ListOptions{After: "a/1", Delimiter: slash},
			objects:  []string{"ab", "c"},
			prefixes: []string{"b/"},
		},
		{
			name: "after a common prefix that equals the marker",
			opts: object.ListOptions{Prefix: "a/", Delimiter: slash, After: "a/x/"},
		},
		{
			name: "no matches",
			opts: object.ListOptions{Prefix: "zzz"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pages := listAllPages(t, s, testBucket, tc.opts)
			got := flattenPages(pages)
			want := sortedCopy(append(append([]string(nil), tc.objects...), tc.prefixes...))
			requireEqualStrings(t, "items", got, want)
			// Each common prefix must be a separate item from objects.
			res, err := s.ListObjects(t.Context(), testBucket, tc.opts)
			mustNoErr(t, err)
			requireEqualStrings(t, "common prefixes", res.CommonPrefixes, tc.prefixes)
		})
	}
}

// caseListHidesNonLatest checks that only the latest data version of each key
// is listed, and that a key whose latest version is a delete marker is hidden.
func caseListHidesNonLatest(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningEnabled)
	mustPutObject(t, s, objectRecord(testBucket, "k1", newVersionID(), "v1"))
	v2 := objectRecord(testBucket, "k1", newVersionID(), "v2")
	mustPutObject(t, s, v2)
	mustPutObject(t, s, objectRecord(testBucket, "k2", newVersionID(), "x"))
	_, _, err := s.DeleteObject(t.Context(), testBucket, "k2", "", object.Conditions{})
	mustNoErr(t, err)

	res, err := s.ListObjects(t.Context(), testBucket, object.ListOptions{})
	mustNoErr(t, err)
	requireCount(t, "listed objects", res.Objects, 1)
	if res.Objects[0].Key != "k1" || res.Objects[0].ETag != etagOf("v2") || res.Objects[0].DeleteMarker {
		t.Fatalf("listed %+v, want the latest data version of k1", res.Objects[0])
	}
}

func caseListPagesWithMaxKeys(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	keys := []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7"}
	populateBucket(t, s, testBucket, keys)

	pages := listAllPages(t, s, testBucket, object.ListOptions{MaxKeys: 3})
	sizes := make([]int, 0, len(pages))
	for _, p := range pages {
		sizes = append(sizes, len(p))
	}
	if fmt.Sprint(sizes) != "[3 3 1]" {
		t.Fatalf("page sizes = %v, want [3 3 1]", sizes)
	}
	requireEqualStrings(t, "paged keys", flattenPages(pages), keys)
}

func caseListInvalidMaxKeys(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	for _, n := range []int{-1, 1001} {
		_, err := s.ListObjects(t.Context(), testBucket, object.ListOptions{MaxKeys: n})
		requireCode(t, err, s3err.InvalidArgument)
	}
}

// caseListUnicodeAndControlKeys checks byte ordering and round trips of keys
// with multi-byte UTF-8 and with the control byte 0x01.
func caseListUnicodeAndControlKeys(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	keys := []string{"a", "a\x01", "a\x01b", "a\x02", "ü/é", "日本語", "🙂", "z/ß"}
	populateBucket(t, s, testBucket, keys)

	pages := listAllPages(t, s, testBucket, object.ListOptions{})
	requireEqualStrings(t, "all keys", flattenPages(pages), sortedCopy(keys))

	pages = listAllPages(t, s, testBucket, object.ListOptions{Prefix: "a\x01"})
	requireEqualStrings(t, "keys under control-byte prefix", flattenPages(pages), []string{"a\x01", "a\x01b"})

	got, err := s.GetObject(t.Context(), testBucket, "日本語", "")
	mustNoErr(t, err)
	if got.Key != "日本語" {
		t.Fatalf("GetObject key = %q", got.Key)
	}
}

// caseListLargePaging lists more than a thousand objects, with and without a
// delimiter, to check that continuation covers every item exactly once.
func caseListLargePaging(t *testing.T, s meta.Store) {
	mustCreateBucket(t, s, testBucket, object.VersioningUnversioned)
	const flat, dirs = 1100, 50
	var keys []string
	for i := range flat {
		keys = append(keys, fmt.Sprintf("obj-%05d", i))
	}
	for i := range dirs {
		keys = append(keys, fmt.Sprintf("dir-%03d/file", i))
	}
	populateBucket(t, s, testBucket, keys)

	t.Run("default page size", func(t *testing.T) {
		pages := listAllPages(t, s, testBucket, object.ListOptions{})
		requireCount(t, "pages", pages, 2)
		requireCount(t, "first page", pages[0], 1000)
		requireCount(t, "second page", pages[1], len(keys)-1000)
		requireEqualStrings(t, "keys", flattenPages(pages), sortedCopy(keys))
	})

	t.Run("delimiter with small pages", func(t *testing.T) {
		var want []string
		for i := range flat {
			want = append(want, fmt.Sprintf("obj-%05d", i))
		}
		for i := range dirs {
			want = append(want, fmt.Sprintf("dir-%03d/", i))
		}
		pages := listAllPages(t, s, testBucket, object.ListOptions{Delimiter: "/", MaxKeys: 64})
		requireEqualStrings(t, "items", flattenPages(pages), sortedCopy(want))
		for _, p := range pages {
			if len(p) > 64 {
				t.Fatalf("page has %d items, want at most 64", len(p))
			}
		}
	})
}

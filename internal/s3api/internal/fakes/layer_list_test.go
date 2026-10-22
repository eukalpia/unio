package fakes

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// listFixtureKeys are the keys of newListFixture in sorted order.
var listFixtureKeys = []string{"a/1", "a/2", "b", "c/1", "c/2", "d"}

// newListFixture returns a layer whose testBucket holds listFixtureKeys.
func newListFixture(t *testing.T) *Layer {
	t.Helper()
	l := newTestLayer(t)
	for _, k := range listFixtureKeys {
		mustPut(t, l, k, []byte("x"))
	}
	return l
}

// walkObjects lists every entry matching opts by following NextAfter, and
// returns the entries as sorted "o:<key>" and "p:<prefix>" strings. It fails
// the test if paging does not terminate.
func walkObjects(t *testing.T, l *Layer, opts object.ListOptions) []string {
	t.Helper()
	var entries []string
	for page := 0; ; page++ {
		if page > 100 {
			t.Fatalf("listing did not terminate with options %+v", opts)
		}
		res, err := l.ListObjects(context.Background(), testBucket, opts)
		wantCode(t, err, "")
		for _, o := range res.Objects {
			entries = append(entries, "o:"+o.Key)
		}
		for _, p := range res.CommonPrefixes {
			entries = append(entries, "p:"+p)
		}
		if !res.IsTruncated {
			break
		}
		if res.NextAfter == "" {
			t.Fatalf("truncated page without NextAfter for options %+v", opts)
		}
		opts.After = res.NextAfter
	}
	slices.Sort(entries)
	return entries
}

func TestListObjects(t *testing.T) {
	tests := []struct {
		name         string
		opts         object.ListOptions
		wantKeys     []string
		wantPrefixes []string
		wantTrunc    bool
		wantNext     string
	}{
		{name: "everything", wantKeys: listFixtureKeys, wantNext: "d"},
		{name: "prefix", opts: object.ListOptions{Prefix: "a/"}, wantKeys: []string{"a/1", "a/2"}, wantNext: "a/2"},
		{
			name:         "delimiter",
			opts:         object.ListOptions{Delimiter: "/"},
			wantKeys:     []string{"b", "d"},
			wantPrefixes: []string{"a/", "c/"},
			wantNext:     "d",
		},
		{
			name:     "prefix with delimiter",
			opts:     object.ListOptions{Prefix: "a/", Delimiter: "/"},
			wantKeys: []string{"a/1", "a/2"},
			wantNext: "a/2",
		},
		{name: "after", opts: object.ListOptions{After: "b"}, wantKeys: []string{"c/1", "c/2", "d"}, wantNext: "d"},
		{
			name:      "max keys truncates",
			opts:      object.ListOptions{MaxKeys: 2},
			wantKeys:  []string{"a/1", "a/2"},
			wantTrunc: true,
			wantNext:  "a/2",
		},
		{
			name:         "max keys counts common prefixes",
			opts:         object.ListOptions{Delimiter: "/", MaxKeys: 2},
			wantKeys:     []string{"b"},
			wantPrefixes: []string{"a/"},
			wantTrunc:    true,
			wantNext:     "b",
		},
		{name: "no match", opts: object.ListOptions{Prefix: "zzz"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newListFixture(t)
			res, err := l.ListObjects(context.Background(), testBucket, tt.opts)
			wantCode(t, err, "")

			var keys []string
			for _, o := range res.Objects {
				keys = append(keys, o.Key)
			}
			if diff := cmp.Diff(tt.wantKeys, keys, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("keys mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantPrefixes, res.CommonPrefixes, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("common prefixes mismatch (-want +got):\n%s", diff)
			}
			if res.IsTruncated != tt.wantTrunc || res.NextAfter != tt.wantNext {
				t.Errorf("IsTruncated=%v NextAfter=%q, want %v and %q", res.IsTruncated, res.NextAfter, tt.wantTrunc, tt.wantNext)
			}
		})
	}
}

func TestListObjectsPagesMatchFullListing(t *testing.T) {
	l := newListFixture(t)
	for _, prefix := range []string{"", "a/"} {
		for _, delimiter := range []string{"", "/"} {
			full := walkObjects(t, l, object.ListOptions{Prefix: prefix, Delimiter: delimiter})
			for maxKeys := 1; maxKeys <= 7; maxKeys++ {
				name := fmt.Sprintf("prefix=%q delimiter=%q max=%d", prefix, delimiter, maxKeys)
				t.Run(name, func(t *testing.T) {
					opts := object.ListOptions{Prefix: prefix, Delimiter: delimiter, MaxKeys: maxKeys}
					got := walkObjects(t, l, opts)
					if diff := cmp.Diff(full, got); diff != "" {
						t.Errorf("paged listing differs from full listing (-full +paged):\n%s", diff)
					}
				})
			}
		}
	}
}

func TestListObjectsPageSizeLimits(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	const total = object.MaxListKeys + 1
	for i := 0; i < total; i++ {
		mustPut(t, l, fmt.Sprintf("k%04d", i), nil)
	}

	tests := []struct {
		name     string
		maxKeys  int
		wantLen  int
		wantCode s3err.Code
	}{
		{name: "zero means maximum", maxKeys: 0, wantLen: object.MaxListKeys},
		{name: "above maximum is capped", maxKeys: 5000, wantLen: object.MaxListKeys},
		{name: "maximum", maxKeys: object.MaxListKeys, wantLen: object.MaxListKeys},
		{name: "negative is rejected", maxKeys: -1, wantCode: s3err.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := l.ListObjects(ctx, testBucket, object.ListOptions{MaxKeys: tt.maxKeys})
			wantCode(t, err, tt.wantCode)
			if tt.wantCode != "" {
				return
			}
			if len(res.Objects) != tt.wantLen || !res.IsTruncated {
				t.Errorf("got %d objects truncated=%v, want %d truncated", len(res.Objects), res.IsTruncated, tt.wantLen)
			}
			if res.NextAfter != res.Objects[len(res.Objects)-1].Key {
				t.Errorf("NextAfter = %q, want the last key returned", res.NextAfter)
			}
		})
	}
}

func TestListObjectsUnknownBucket(t *testing.T) {
	_, err := NewLayer().ListObjects(context.Background(), "missing-bucket", object.ListOptions{})
	wantCode(t, err, s3err.NoSuchBucket)
}

// listedUpload is an upload created by newUploadListFixture.
type listedUpload struct {
	key string
	id  string
}

// newUploadListFixture starts several uploads in testBucket and returns them
// sorted by key and then upload ID, the order ListMultipartUploads uses.
func newUploadListFixture(t *testing.T) (*Layer, []listedUpload) {
	t.Helper()
	l := newTestLayer(t)
	var uploads []listedUpload
	for _, k := range []string{"a/x", "a/x", "a/y", "b", "c/z"} {
		uploads = append(uploads, listedUpload{key: k, id: mustUpload(t, l, k, object.PutOptions{})})
	}
	slices.SortFunc(uploads, func(a, b listedUpload) int {
		if a.key != b.key {
			if a.key < b.key {
				return -1
			}
			return 1
		}
		if a.id < b.id {
			return -1
		}
		if a.id > b.id {
			return 1
		}
		return 0
	})
	return l, uploads
}

// walkUploads lists every upload matching opts by following the key and
// upload ID markers. Entries are returned sorted as "u:<key>:<id>" and
// "p:<prefix>" strings.
func walkUploads(t *testing.T, l *Layer, opts object.ListUploadsOptions) []string {
	t.Helper()
	var entries []string
	for page := 0; ; page++ {
		if page > 100 {
			t.Fatalf("listing did not terminate with options %+v", opts)
		}
		res, err := l.ListMultipartUploads(context.Background(), testBucket, opts)
		wantCode(t, err, "")
		for _, u := range res.Uploads {
			entries = append(entries, "u:"+u.Key+":"+u.UploadID)
		}
		for _, p := range res.CommonPrefixes {
			entries = append(entries, "p:"+p)
		}
		if !res.IsTruncated {
			break
		}
		opts.KeyMarker = res.NextKeyMarker
		opts.UploadIDMarker = res.NextUploadIDMarker
	}
	slices.Sort(entries)
	return entries
}

func TestListMultipartUploads(t *testing.T) {
	l, uploads := newUploadListFixture(t)
	var all []string
	for _, u := range uploads {
		all = append(all, u.key)
	}

	ctx := context.Background()
	res, err := l.ListMultipartUploads(ctx, testBucket, object.ListUploadsOptions{})
	wantCode(t, err, "")
	var gotKeys []string
	for i, u := range res.Uploads {
		gotKeys = append(gotKeys, u.Key)
		if u.UploadID != uploads[i].id || u.Key != uploads[i].key {
			t.Errorf("upload %d = %s/%s, want %s/%s", i, u.Key, u.UploadID, uploads[i].key, uploads[i].id)
		}
		if u.Initiated == 0 {
			t.Errorf("upload %d has no Initiated time", i)
		}
	}
	if diff := cmp.Diff(all, gotKeys); diff != "" {
		t.Errorf("upload keys mismatch (-want +got):\n%s", diff)
	}

	tests := []struct {
		name         string
		opts         object.ListUploadsOptions
		wantKeys     []string
		wantPrefixes []string
	}{
		{name: "prefix", opts: object.ListUploadsOptions{Prefix: "a/"}, wantKeys: []string{"a/x", "a/x", "a/y"}},
		{
			name:         "delimiter",
			opts:         object.ListUploadsOptions{Delimiter: "/"},
			wantKeys:     []string{"b"},
			wantPrefixes: []string{"a/", "c/"},
		},
		{
			name:     "prefix with delimiter",
			opts:     object.ListUploadsOptions{Prefix: "a/", Delimiter: "/"},
			wantKeys: []string{"a/x", "a/x", "a/y"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := l.ListMultipartUploads(ctx, testBucket, tt.opts)
			wantCode(t, err, "")
			var keys []string
			for _, u := range res.Uploads {
				keys = append(keys, u.Key)
			}
			if diff := cmp.Diff(tt.wantKeys, keys, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("keys mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantPrefixes, res.CommonPrefixes, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("common prefixes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestListMultipartUploadsPagesMatchFullListing(t *testing.T) {
	l, _ := newUploadListFixture(t)
	for _, delimiter := range []string{"", "/"} {
		full := walkUploads(t, l, object.ListUploadsOptions{Delimiter: delimiter})
		for maxUploads := 1; maxUploads <= 6; maxUploads++ {
			name := fmt.Sprintf("delimiter=%q max=%d", delimiter, maxUploads)
			t.Run(name, func(t *testing.T) {
				got := walkUploads(t, l, object.ListUploadsOptions{Delimiter: delimiter, MaxUploads: maxUploads})
				if diff := cmp.Diff(full, got); diff != "" {
					t.Errorf("paged uploads differ from full listing (-full +paged):\n%s", diff)
				}
			})
		}
	}
}

func TestListMultipartUploadsErrors(t *testing.T) {
	l := newTestLayer(t)
	_, err := l.ListMultipartUploads(context.Background(), testBucket, object.ListUploadsOptions{MaxUploads: -1})
	wantCode(t, err, s3err.InvalidArgument)
	_, err = l.ListMultipartUploads(context.Background(), "missing-bucket", object.ListUploadsOptions{})
	wantCode(t, err, s3err.NoSuchBucket)
}

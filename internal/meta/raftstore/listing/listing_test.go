package listing

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

var errBoom = errors.New("boom")

// pair is the (key, upload id) of an upload.
type pair struct{ key, id string }

func objectKeys(objs []meta.ObjectRecord) []string {
	var out []string
	for _, o := range objs {
		out = append(out, o.Key)
	}
	return out
}

func uploadPairs(ups []object.Upload) []pair {
	var out []pair
	for _, u := range ups {
		out = append(out, pair{u.Key, u.UploadID})
	}
	return out
}

// pageRowsEqual fails the test unless the page holds exactly the oracle rows.
func pageRowsEqual(t *testing.T, label string, got, want []row) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: page = %v, want %v", label, got, want)
	}
}

func TestListObjects(t *testing.T) {
	tests := []struct {
		name          string
		keys          []string
		parts         int
		prefix        string
		delim         string
		after         string
		maxKeys       int
		wantObjects   []string
		wantPrefixes  []string
		wantTruncated bool
		wantNext      string
	}{
		{name: "no partitions"},
		{name: "empty partitions", parts: 3},
		{
			name:        "single partition",
			keys:        []string{"a", "b", "c"},
			parts:       1,
			wantObjects: []string{"a", "b", "c"},
			wantNext:    "c",
		},
		{
			name:        "keys interleaved over seven partitions",
			keys:        seq("k%02d", 50),
			parts:       7,
			wantObjects: seq("k%02d", 50),
			wantNext:    "k49",
		},
		{
			name:         "prefixes shared across partitions are emitted once",
			keys:         []string{"a/1", "a/2", "a/3", "b/1", "b/2", "c", "d/x/1", "d/x/2"},
			parts:        3,
			delim:        "/",
			wantObjects:  []string{"c"},
			wantPrefixes: []string{"a/", "b/", "d/"},
			wantNext:     "d/",
		},
		{
			name:         "prefix and delimiter",
			keys:         []string{"x/a/1", "x/a/2", "x/b", "x/c/d", "y"},
			parts:        2,
			prefix:       "x/",
			delim:        "/",
			wantObjects:  []string{"x/b"},
			wantPrefixes: []string{"x/a/", "x/c/"},
			wantNext:     "x/c/",
		},
		{
			name:        "bytewise order of 0x01 and unicode keys",
			keys:        []string{"é/x", "z", "a\x01b", "a", "a\x01", "日本", "a\x02"},
			parts:       2,
			wantObjects: []string{"a", "a\x01", "a\x01b", "a\x02", "z", "é/x", "日本"},
			wantNext:    "日本",
		},
		{
			name:         "0x01 as the delimiter",
			keys:         []string{"a\x01b", "a\x01c", "b", "é/x"},
			parts:        2,
			delim:        "\x01",
			wantObjects:  []string{"b", "é/x"},
			wantPrefixes: []string{"a\x01"},
			wantNext:     "é/x",
		},
		{
			name:        "after inside a common prefix",
			keys:        []string{"a/1", "a/2", "a/3", "b"},
			parts:       2,
			delim:       "/",
			after:       "a/2",
			wantObjects: []string{"b"},
			wantNext:    "b",
		},
		{
			name:        "after equal to a common prefix",
			keys:        []string{"a/1", "a/2", "b"},
			parts:       2,
			delim:       "/",
			after:       "a/",
			wantObjects: []string{"b"},
			wantNext:    "b",
		},
		{
			name:        "after an object",
			keys:        []string{"a", "b", "c"},
			parts:       2,
			after:       "b",
			wantObjects: []string{"c"},
			wantNext:    "c",
		},
		{
			name:  "after the last key",
			keys:  []string{"a", "b"},
			parts: 2,
			after: "z",
		},
		{
			name:          "first page truncated",
			keys:          []string{"a", "b", "c", "d", "e"},
			parts:         2,
			maxKeys:       2,
			wantObjects:   []string{"a", "b"},
			wantTruncated: true,
			wantNext:      "b",
		},
		{
			name:        "page ends exactly at the total",
			keys:        []string{"a", "b"},
			parts:       2,
			maxKeys:     2,
			wantObjects: []string{"a", "b"},
			wantNext:    "b",
		},
		{
			name:         "duplicate prefix as the lookahead is not truncation",
			keys:         []string{"a/1", "a/2"},
			parts:        2,
			delim:        "/",
			maxKeys:      1,
			wantPrefixes: []string{"a/"},
			wantNext:     "a/",
		},
		{
			name:          "page ends on a prefix and more follow",
			keys:          []string{"a/1", "b"},
			parts:         2,
			delim:         "/",
			maxKeys:       1,
			wantPrefixes:  []string{"a/"},
			wantTruncated: true,
			wantNext:      "a/",
		},
		{
			name:          "page mixes objects and prefixes",
			keys:          []string{"a", "a/1", "b", "c/1", "d"},
			parts:         2,
			delim:         "/",
			maxKeys:       3,
			wantObjects:   []string{"a", "b"},
			wantPrefixes:  []string{"a/"},
			wantTruncated: true,
			wantNext:      "b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore(spread(objects(tt.keys...), tt.parts, tt.prefix, tt.delim))
			res, err := ListObjects(context.Background(), partIDs(tt.parts), store.fetch, object.ListOptions{
				Prefix:    tt.prefix,
				Delimiter: tt.delim,
				After:     tt.after,
				MaxKeys:   tt.maxKeys,
			})
			if err != nil {
				t.Fatalf("ListObjects: %v", err)
			}
			if got := objectKeys(res.Objects); !slices.Equal(got, tt.wantObjects) {
				t.Errorf("objects = %q, want %q", got, tt.wantObjects)
			}
			if !slices.Equal(res.CommonPrefixes, tt.wantPrefixes) {
				t.Errorf("prefixes = %q, want %q", res.CommonPrefixes, tt.wantPrefixes)
			}
			if res.IsTruncated != tt.wantTruncated {
				t.Errorf("IsTruncated = %v, want %v", res.IsTruncated, tt.wantTruncated)
			}
			if res.NextAfter != tt.wantNext {
				t.Errorf("NextAfter = %q, want %q", res.NextAfter, tt.wantNext)
			}
		})
	}
}

// TestListObjectsTruncationBoundaries checks IsTruncated exactly at the page
// size equal to the total, one below it and one above it.
func TestListObjectsTruncationBoundaries(t *testing.T) {
	for total := range 13 {
		keys := seq("k%02d", total)
		for _, maxKeys := range []int{total - 1, total, total + 1} {
			if maxKeys < 1 {
				continue
			}
			t.Run(fmt.Sprintf("total=%d/max=%d", total, maxKeys), func(t *testing.T) {
				store := newFakeStore(spread(objects(keys...), 3, "", ""))
				res, err := ListObjects(context.Background(), partIDs(3), store.fetch, object.ListOptions{MaxKeys: maxKeys})
				if err != nil {
					t.Fatalf("ListObjects: %v", err)
				}
				if got, want := len(res.Objects), min(total, maxKeys); got != want {
					t.Errorf("len(objects) = %d, want %d", got, want)
				}
				if want := maxKeys < total; res.IsTruncated != want {
					t.Errorf("IsTruncated = %v, want %v", res.IsTruncated, want)
				}
			})
		}
	}
}

// checkObjectPages pages through the listing with size, following NextAfter
// until the listing ends, and checks every page against the oracle.
func checkObjectPages(t *testing.T, store *fakeStore, parts []ids.PartitionID, oracle []row, prefix, delim string, size int) {
	t.Helper()
	after := ""
	for pages := 0; ; pages++ {
		if pages > len(oracle)+1 {
			t.Fatalf("size %d: paging did not terminate", size)
		}
		res, err := ListObjects(context.Background(), parts, store.fetch, object.ListOptions{
			Prefix: prefix, Delimiter: delim, After: after, MaxKeys: size,
		})
		if err != nil {
			t.Fatalf("size %d after %q: %v", size, after, err)
		}
		want, truncated := pageAfter(oracle, after, size)
		pageRowsEqual(t, fmt.Sprintf("size %d after %q", size, after), objectPageRows(res), want)
		if res.IsTruncated != truncated {
			t.Fatalf("size %d after %q: IsTruncated = %v, want %v", size, after, res.IsTruncated, truncated)
		}
		wantNext := ""
		if len(want) > 0 {
			wantNext = want[len(want)-1].sort
		}
		if res.NextAfter != wantNext {
			t.Fatalf("size %d after %q: NextAfter = %q, want %q", size, after, res.NextAfter, wantNext)
		}
		if !res.IsTruncated {
			return
		}
		after = res.NextAfter
	}
}

// TestListObjectsPagesMatchListing pages random listings over several
// partition counts, delimiters and prefixes, and checks every page of every
// size against the single-partition listing.
func TestListObjectsPagesMatchListing(t *testing.T) {
	for _, parts := range []int{1, 3, 16} {
		for _, delim := range []string{"", "/", "\x01"} {
			for _, prefix := range []string{"", "a"} {
				t.Run(fmt.Sprintf("parts=%d/delim=%q/prefix=%q", parts, delim, prefix), func(t *testing.T) {
					entries := randomEntries(rand.New(rand.NewPCG(uint64(parts), 42)), 60, false)
					oracle := rowsOf(listingOf(entries, prefix, delim))
					store := newFakeStore(spread(entries, parts, prefix, delim))
					for _, size := range pageSizes(len(oracle)) {
						checkObjectPages(t, store, partIDs(parts), oracle, prefix, delim, size)
					}
				})
			}
		}
	}
}

func TestListUploads(t *testing.T) {
	tests := []struct {
		name           string
		entries        []entry
		parts          int
		prefix         string
		delim          string
		keyMarker      string
		uploadIDMarker string
		maxUploads     int
		wantUploads    []pair
		wantPrefixes   []string
		wantTruncated  bool
		wantKeyMarker  string
		wantIDMarker   string
	}{
		{name: "no uploads", parts: 2},
		{
			name:          "one key with several uploads",
			entries:       uploads(pair{"a", "2"}, pair{"a", "1"}, pair{"b", "1"}),
			parts:         1,
			wantUploads:   []pair{{"a", "1"}, {"a", "2"}, {"b", "1"}},
			wantKeyMarker: "b",
			wantIDMarker:  "1",
		},
		{
			name:          "same key on several partitions",
			entries:       uploads(pair{"k", "1"}, pair{"k", "2"}, pair{"k", "3"}),
			parts:         2,
			wantUploads:   []pair{{"k", "1"}, {"k", "2"}, {"k", "3"}},
			wantKeyMarker: "k",
			wantIDMarker:  "3",
		},
		{
			name:          "NUL separator orders keys with 0x01 correctly",
			entries:       uploads(pair{"a\x01", "1"}, pair{"a", "z"}, pair{"a", "b"}),
			parts:         2,
			wantUploads:   []pair{{"a", "b"}, {"a", "z"}, {"a\x01", "1"}},
			wantKeyMarker: "a\x01",
			wantIDMarker:  "1",
		},
		{
			name:          "common prefixes across partitions are emitted once",
			entries:       uploads(pair{"a/x", "1"}, pair{"a/y", "2"}, pair{"b", "1"}, pair{"c/d", "2"}, pair{"c/e", "9"}),
			parts:         2,
			delim:         "/",
			wantUploads:   []pair{{"b", "1"}},
			wantPrefixes:  []string{"a/", "c/"},
			wantKeyMarker: "c/",
		},
		{
			name:          "prefix and delimiter",
			entries:       uploads(pair{"x/a", "1"}, pair{"x/b", "1"}, pair{"y", "1"}),
			parts:         2,
			prefix:        "x/",
			delim:         "/",
			wantUploads:   []pair{{"x/a", "1"}, {"x/b", "1"}},
			wantKeyMarker: "x/b",
			wantIDMarker:  "1",
		},
		{
			name:           "marker inside a key",
			entries:        uploads(pair{"a", "1"}, pair{"b", "1"}, pair{"b", "2"}, pair{"b", "3"}, pair{"c", "1"}),
			parts:          2,
			keyMarker:      "b",
			uploadIDMarker: "2",
			wantUploads:    []pair{{"b", "3"}, {"c", "1"}},
			wantKeyMarker:  "c",
			wantIDMarker:   "1",
		},
		{
			name:          "marker without an upload id",
			entries:       uploads(pair{"a", "1"}, pair{"b", "1"}, pair{"b", "2"}, pair{"b", "3"}, pair{"c", "1"}),
			parts:         2,
			keyMarker:     "b",
			wantUploads:   []pair{{"b", "1"}, {"b", "2"}, {"b", "3"}, {"c", "1"}},
			wantKeyMarker: "c",
			wantIDMarker:  "1",
		},
		{
			name:           "upload id marker without key marker is ignored",
			entries:        uploads(pair{"a", "1"}, pair{"b", "1"}, pair{"b", "2"}),
			parts:          2,
			uploadIDMarker: "2",
			wantUploads:    []pair{{"a", "1"}, {"b", "1"}, {"b", "2"}},
			wantKeyMarker:  "b",
			wantIDMarker:   "2",
		},
		{
			name:          "marker that is a common prefix",
			entries:       uploads(pair{"a/x", "1"}, pair{"b", "1"}),
			parts:         2,
			delim:         "/",
			keyMarker:     "a/",
			wantUploads:   []pair{{"b", "1"}},
			wantKeyMarker: "b",
			wantIDMarker:  "1",
		},
		{
			name:      "marker past the end",
			entries:   uploads(pair{"a", "1"}),
			parts:     2,
			keyMarker: "z",
		},
		{
			name:          "page truncated after an upload",
			entries:       uploads(pair{"a", "1"}, pair{"a", "2"}, pair{"b", "1"}),
			parts:         2,
			maxUploads:    2,
			wantUploads:   []pair{{"a", "1"}, {"a", "2"}},
			wantTruncated: true,
			wantKeyMarker: "a",
			wantIDMarker:  "2",
		},
		{
			name:          "page ends on a common prefix",
			entries:       uploads(pair{"a/x", "1"}, pair{"b", "1"}),
			parts:         2,
			delim:         "/",
			maxUploads:    1,
			wantPrefixes:  []string{"a/"},
			wantTruncated: true,
			wantKeyMarker: "a/",
		},
		{
			name:          "duplicate prefix as the lookahead is not truncation",
			entries:       uploads(pair{"a/x", "1"}, pair{"a/y", "2"}),
			parts:         2,
			delim:         "/",
			maxUploads:    1,
			wantPrefixes:  []string{"a/"},
			wantKeyMarker: "a/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore(spread(tt.entries, tt.parts, tt.prefix, tt.delim))
			res, err := ListUploads(context.Background(), partIDs(tt.parts), store.fetch, object.ListUploadsOptions{
				Prefix:         tt.prefix,
				Delimiter:      tt.delim,
				KeyMarker:      tt.keyMarker,
				UploadIDMarker: tt.uploadIDMarker,
				MaxUploads:     tt.maxUploads,
			})
			if err != nil {
				t.Fatalf("ListUploads: %v", err)
			}
			wantStart := ""
			if tt.keyMarker != "" {
				wantStart = UploadCursor(tt.keyMarker, tt.uploadIDMarker)
			}
			for _, p := range partIDs(tt.parts) {
				if c, ok := store.firstCursor(p); !ok || c != wantStart {
					t.Errorf("partition %d first cursor = %q, want %q", p, c, wantStart)
				}
			}
			if got := uploadPairs(res.Uploads); !slices.Equal(got, tt.wantUploads) {
				t.Errorf("uploads = %v, want %v", got, tt.wantUploads)
			}
			if !slices.Equal(res.CommonPrefixes, tt.wantPrefixes) {
				t.Errorf("prefixes = %q, want %q", res.CommonPrefixes, tt.wantPrefixes)
			}
			if res.IsTruncated != tt.wantTruncated {
				t.Errorf("IsTruncated = %v, want %v", res.IsTruncated, tt.wantTruncated)
			}
			if res.NextKeyMarker != tt.wantKeyMarker {
				t.Errorf("NextKeyMarker = %q, want %q", res.NextKeyMarker, tt.wantKeyMarker)
			}
			if res.NextUploadIDMarker != tt.wantIDMarker {
				t.Errorf("NextUploadIDMarker = %q, want %q", res.NextUploadIDMarker, tt.wantIDMarker)
			}
		})
	}
}

// checkUploadPages pages through the uploads with size, following the
// key and upload id markers, and checks every page against the oracle.
func checkUploadPages(t *testing.T, store *fakeStore, parts []ids.PartitionID, oracle []row, prefix, delim string, size int) {
	t.Helper()
	keyMarker, idMarker := "", ""
	for pages := 0; ; pages++ {
		if pages > len(oracle)+1 {
			t.Fatalf("size %d: paging did not terminate", size)
		}
		res, err := ListUploads(context.Background(), parts, store.fetch, object.ListUploadsOptions{
			Prefix: prefix, Delimiter: delim, KeyMarker: keyMarker, UploadIDMarker: idMarker, MaxUploads: size,
		})
		if err != nil {
			t.Fatalf("size %d after %q/%q: %v", size, keyMarker, idMarker, err)
		}
		after := ""
		if keyMarker != "" {
			after = UploadCursor(keyMarker, idMarker)
		}
		want, truncated := pageAfter(oracle, after, size)
		pageRowsEqual(t, fmt.Sprintf("size %d after %q", size, after), uploadPageRows(res), want)
		if res.IsTruncated != truncated {
			t.Fatalf("size %d after %q: IsTruncated = %v, want %v", size, after, res.IsTruncated, truncated)
		}
		if len(want) > 0 {
			// The markers must name the last entry of the page: a common prefix
			// by itself, an upload by its cursor.
			last := want[len(want)-1]
			gotCursor := UploadCursor(res.NextKeyMarker, res.NextUploadIDMarker)
			if last.cp {
				gotCursor = res.NextKeyMarker
				if res.NextUploadIDMarker != "" {
					t.Fatalf("size %d after %q: NextUploadIDMarker = %q after a common prefix", size, after, res.NextUploadIDMarker)
				}
			}
			if gotCursor != last.sort {
				t.Fatalf("size %d after %q: markers name %q, want %q", size, after, gotCursor, last.sort)
			}
		}
		if !res.IsTruncated {
			return
		}
		keyMarker, idMarker = res.NextKeyMarker, res.NextUploadIDMarker
	}
}

// TestListUploadsPagesMatchListing is the uploads counterpart of
// TestListObjectsPagesMatchListing.
func TestListUploadsPagesMatchListing(t *testing.T) {
	for _, parts := range []int{1, 3, 16} {
		for _, delim := range []string{"", "/", "\x01"} {
			for _, prefix := range []string{"", "a"} {
				t.Run(fmt.Sprintf("parts=%d/delim=%q/prefix=%q", parts, delim, prefix), func(t *testing.T) {
					entries := randomEntries(rand.New(rand.NewPCG(uint64(parts), 7)), 60, true)
					oracle := rowsOf(listingOf(entries, prefix, delim))
					store := newFakeStore(spread(entries, parts, prefix, delim))
					for _, size := range pageSizes(len(oracle)) {
						checkUploadPages(t, store, partIDs(parts), oracle, prefix, delim, size)
					}
				})
			}
		}
	}
}

// TestUploadInitiated checks that Initiated is the record's time in unix nanoseconds.
func TestUploadInitiated(t *testing.T) {
	const ts = int64(1700000000123456789)
	store := newFakeStore(map[ids.PartitionID][]Item{
		0: listingOf([]entry{{key: "a", uploadID: "1", ts: ts}}, "", ""),
	})
	res, err := ListUploads(context.Background(), partIDs(1), store.fetch, object.ListUploadsOptions{})
	if err != nil {
		t.Fatalf("ListUploads: %v", err)
	}
	if len(res.Uploads) != 1 || res.Uploads[0].Initiated != ts {
		t.Fatalf("uploads = %+v, want one upload initiated at %d", res.Uploads, ts)
	}
}

func TestListingErrors(t *testing.T) {
	keys := seq("k%03d", 300)
	bigSplit := func() map[ids.PartitionID][]Item {
		return map[ids.PartitionID][]Item{
			0: listingOf(objects(keys...), "", ""),
			1: listingOf(objects("z"), "", ""),
		}
	}
	ctxCanceled := func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	}

	t.Run("page size out of range", func(t *testing.T) {
		for _, n := range []int{-1, object.MaxListKeys + 1} {
			store := newFakeStore(bigSplit())
			if _, err := ListObjects(context.Background(), partIDs(2), store.fetch, object.ListOptions{MaxKeys: n}); !s3err.Is(err, s3err.InvalidArgument) {
				t.Errorf("ListObjects MaxKeys=%d: err = %v, want InvalidArgument", n, err)
			}
			if _, err := ListUploads(context.Background(), partIDs(2), store.fetch, object.ListUploadsOptions{MaxUploads: n}); !s3err.Is(err, s3err.InvalidArgument) {
				t.Errorf("ListUploads MaxUploads=%d: err = %v, want InvalidArgument", n, err)
			}
			if got := store.callCount(0) + store.callCount(1); got != 0 {
				t.Errorf("MaxKeys=%d: %d fetches before validation", n, got)
			}
		}
	})

	t.Run("first fetch fails", func(t *testing.T) {
		store := newFakeStore(bigSplit())
		store.inject = func(p ids.PartitionID, _ int) error {
			if p == 1 {
				return errBoom
			}
			return nil
		}
		_, err := ListObjects(context.Background(), partIDs(2), store.fetch, object.ListOptions{MaxKeys: 5})
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom", err)
		}
		if !strings.HasPrefix(err.Error(), "listing: ") {
			t.Errorf("err = %q, want the listing prefix", err)
		}
	})

	t.Run("refill fails", func(t *testing.T) {
		store := newFakeStore(bigSplit())
		store.inject = func(p ids.PartitionID, call int) error {
			if p == 0 && call == 2 {
				return errBoom
			}
			return nil
		}
		_, err := ListObjects(context.Background(), partIDs(2), store.fetch, object.ListOptions{MaxKeys: 300})
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom", err)
		}
	})

	t.Run("uploads fetch fails", func(t *testing.T) {
		store := newFakeStore(map[ids.PartitionID][]Item{0: listingOf(uploads(pair{"a", "1"}), "", "")})
		store.inject = func(ids.PartitionID, int) error { return errBoom }
		_, err := ListUploads(context.Background(), partIDs(1), store.fetch, object.ListUploadsOptions{})
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom", err)
		}
	})

	t.Run("canceled before the call", func(t *testing.T) {
		ctx, cancel := ctxCanceled()
		defer cancel()
		store := newFakeStore(bigSplit())
		_, err := ListObjects(ctx, partIDs(2), store.fetch, object.ListOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if got := store.callCount(0) + store.callCount(1); got != 0 {
			t.Errorf("%d fetches after cancellation", got)
		}
	})

	t.Run("canceled during the first fetches", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store := newFakeStore(bigSplit())
		store.inject = func(ids.PartitionID, int) error {
			cancel()
			return nil
		}
		_, err := ListObjects(ctx, partIDs(2), store.fetch, object.ListOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})

	t.Run("canceled during a refill", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store := newFakeStore(bigSplit())
		store.inject = func(p ids.PartitionID, call int) error {
			if p == 0 && call == 2 {
				cancel()
			}
			return nil
		}
		_, err := ListObjects(ctx, partIDs(2), store.fetch, object.ListOptions{MaxKeys: 300})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

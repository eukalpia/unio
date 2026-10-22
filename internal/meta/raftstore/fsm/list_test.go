package fsm

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// listed is one listing item: an object key or a common prefix, and for an
// upload its upload id. The model and the FSM both produce this shape.
type listed struct {
	name   string
	id     string
	prefix bool
}

// groupItems is the listing the spec defines, computed without the FSM. Each
// input key is an item, unless the text after prefix contains the delimiter,
// in which case it belongs to the common prefix up to and including the first
// delimiter. Items are sorted by name, then id.
func groupItems(in []listed, prefix, delim string) []listed {
	seen := make(map[string]bool)
	var out []listed
	for _, it := range in {
		if !strings.HasPrefix(it.name, prefix) {
			continue
		}
		rest := it.name[len(prefix):]
		if delim != "" {
			if i := strings.Index(rest, delim); i >= 0 {
				cp := prefix + rest[:i+len(delim)]
				if !seen[cp] {
					seen[cp] = true
					out = append(out, listed{name: cp, prefix: true})
				}
				continue
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].name != out[j].name {
			return out[i].name < out[j].name
		}
		return out[i].id < out[j].id
	})
	return out
}

// pastCursor reports whether an item comes strictly after the cursor. A
// common prefix compares by its name; an object or upload compares by
// (name, id). An object cursor has an empty id.
func pastCursor(it listed, km, uim string) bool {
	if it.prefix || it.name != km {
		return it.name > km
	}
	return it.id > uim
}

// filterAfter keeps the items strictly after the cursor.
func filterAfter(items []listed, km, uim string) []listed {
	var out []listed
	for _, it := range items {
		if pastCursor(it, km, uim) {
			out = append(out, it)
		}
	}
	return out
}

// pages follows the cursor from the start and returns every item. Each page
// must hold at most limit items, a page that is not the last must hold
// exactly limit items, and every item must come strictly after the cursor it
// was requested with. The last check also stops a broken cursor from looping.
func pages(t *testing.T, limit uint32, fetch func(km, uim string) ([]listed, bool)) []listed {
	t.Helper()
	var all []listed
	km, uim := "", ""
	for {
		page, exhausted := fetch(km, uim)
		for _, it := range page {
			if !pastCursor(it, km, uim) {
				t.Fatalf("item %+v is not after the cursor (%q, %q)", it, km, uim)
			}
		}
		if uint32(len(page)) > limit {
			t.Fatalf("page of %d items exceeds limit %d", len(page), limit)
		}
		if !exhausted && uint32(len(page)) != limit {
			t.Fatalf("page of %d items is not exhausted but is short of limit %d", len(page), limit)
		}
		all = append(all, page...)
		if exhausted {
			return all
		}
		last := page[len(page)-1]
		km, uim = last.name, last.id
	}
}

// assertListing fails unless got equals want, item by item.
func assertListing(t *testing.T, got, want []listed) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("listed %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// listObjects runs one ListObjects page.
func (h *harness) listObjects(prefix, delim, after string, limit uint32) *pb.ListObjectsResult {
	h.t.Helper()
	res, err := h.read(&pb.ReadQuery{Query: &pb.ReadQuery_ListObjects{ListObjects: &pb.ListObjectsQuery{
		BucketId:  uint64(testBucket),
		Prefix:    []byte(prefix),
		Delimiter: []byte(delim),
		After:     []byte(after),
		Limit:     limit,
	}}})
	if err != nil {
		h.t.Fatalf("list objects: %v", err)
	}
	return res.GetListObjects()
}

// listUploads runs one ListUploads page.
func (h *harness) listUploads(prefix, delim, keyMarker, uploadIDMarker string, limit uint32) *pb.ListUploadsResult {
	h.t.Helper()
	res, err := h.read(&pb.ReadQuery{Query: &pb.ReadQuery_ListUploads{ListUploads: &pb.ListUploadsQuery{
		BucketId:       uint64(testBucket),
		Prefix:         []byte(prefix),
		Delimiter:      []byte(delim),
		KeyMarker:      keyMarker,
		UploadIdMarker: uploadIDMarker,
		Limit:          limit,
	}}})
	if err != nil {
		h.t.Fatalf("list uploads: %v", err)
	}
	return res.GetListUploads()
}

// objectFixture is a bucket of more than 1000 keys: several prefixes, two or
// three versions per key, delete markers, and keys with NUL, 0x01, 0xFF and
// unicode bytes. visible holds the keys whose latest version is a live object,
// sorted in byte order.
type objectFixture struct {
	h       *harness
	visible []listed
}

func buildObjectFixture(t *testing.T) *objectFixture {
	t.Helper()
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, enabled))
	var visible []listed
	for d := 0; d < 6; d++ {
		for s := 0; s < 5; s++ {
			for o := 0; o < 40; o++ {
				key := fmt.Sprintf("dir%02d/sub%02d/obj%03d", d, s, o)
				h.mustApply(putCmd(testBucket, objRec(key, "v1", "e1-"+key), nil))
				h.mustApply(putCmd(testBucket, objRec(key, "v2", "e2-"+key), nil))
				// Every sub-directory keeps obj000 live, so no common prefix is
				// made of delete markers alone.
				if o%9 == 4 {
					h.mustApply(deleteCmd(testBucket, key, "", "m1", nil))
					continue
				}
				visible = append(visible, listed{name: key})
			}
		}
	}
	special := []string{"k\x02x", "k\x01", "k\x02\x01", "\x01", "k\x02/z", "é/ü", "日本/語", "x/\x7f", "x/\x7f\x7f"}
	for _, key := range special {
		h.mustApply(putCmd(testBucket, objRec(key, "v1", "e1"), nil))
		h.mustApply(putCmd(testBucket, objRec(key, "v2", "e2"), nil))
		visible = append(visible, listed{name: key})
	}
	for _, vid := range []string{"v1", "v2", "v3"} {
		h.mustApply(putCmd(testBucket, objRec("multi", vid, "e-"+vid), nil))
	}
	visible = append(visible, listed{name: "multi"})

	// A key whose only version is a delete marker.
	h.mustApply(putCmd(testBucket, objRec("gone", "v1", "e1"), nil))
	h.mustApply(deleteCmd(testBucket, "gone", "", "m1", nil))

	sort.Slice(visible, func(i, j int) bool { return visible[i].name < visible[j].name })
	if len(visible) < 1000 {
		t.Fatalf("fixture has %d live keys, want at least 1000", len(visible))
	}
	return &objectFixture{h: h, visible: visible}
}

// fetch returns a ListObjects page function for pages.
func (f *objectFixture) fetch(prefix, delim string, limit uint32) func(km, uim string) ([]listed, bool) {
	return func(km, _ string) ([]listed, bool) {
		res := f.h.listObjects(prefix, delim, km, limit)
		out := make([]listed, 0, len(res.GetItems()))
		for _, item := range res.GetItems() {
			if obj := item.GetObject(); obj != nil {
				out = append(out, listed{name: obj.Key})
			} else {
				out = append(out, listed{name: item.GetCommonPrefix(), prefix: true})
			}
		}
		return out, res.GetExhausted()
	}
}

func TestListObjects(t *testing.T) {
	f := buildObjectFixture(t)

	t.Run("pages equal the model", func(t *testing.T) {
		tests := []struct {
			name          string
			prefix, delim string
			limit         uint32
		}{
			{name: "all keys, one at a time", limit: 1},
			{name: "all keys, pages of 13", limit: 13},
			{name: "delimiter, one at a time", delim: "/", limit: 1},
			{name: "delimiter, pages of 5", delim: "/", limit: 5},
			{name: "delimiter, single page", delim: "/", limit: maxReadLimit},
			{name: "prefix ends at a level", prefix: "dir02/", delim: "/", limit: 3},
			{name: "prefix inside a level", prefix: "dir02/sub03/", limit: 7},
			{name: "prefix cuts a name", prefix: "dir0", delim: "/", limit: 2},
			{name: "NUL delimiter", prefix: "k", delim: "\x00", limit: 2},
			{name: "0x01 delimiter", delim: "\x01", limit: 1000},
			{name: "0xFF keys", prefix: "x/", delim: "/", limit: 1},
			{name: "empty result", prefix: "zzz", limit: 1},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got := pages(t, tc.limit, f.fetch(tc.prefix, tc.delim, tc.limit))
				assertListing(t, got, groupItems(f.visible, tc.prefix, tc.delim))
			})
		}
	})

	t.Run("cursor inside a common prefix", func(t *testing.T) {
		// dir03/ sorts before this cursor, so the group is not returned again,
		// and the keys under it are not returned either.
		after := "dir03/sub02/obj005"
		res := f.h.listObjects("", "/", after, maxReadLimit)
		var got []listed
		for _, item := range res.GetItems() {
			if item.GetObject() != nil {
				got = append(got, listed{name: item.GetObject().Key})
			} else {
				got = append(got, listed{name: item.GetCommonPrefix(), prefix: true})
			}
		}
		want := filterAfter(groupItems(f.visible, "", "/"), after, "")
		assertListing(t, got, want)
		if len(got) == 0 || got[0].name != "dir04/" {
			t.Fatalf("first item after cursor = %v, want dir04/", got)
		}
	})

	t.Run("cursors across the whole keyspace", func(t *testing.T) {
		all := groupItems(f.visible, "", "")
		for _, after := range []string{"", "dir00/sub00/obj000", "dir00/sub00/obj004", "dir05/sub04/obj039", "k\x02", "k\x02\x01", "zzz"} {
			res := f.h.listObjects("", "", after, maxReadLimit)
			var got []listed
			for _, item := range res.GetItems() {
				got = append(got, listed{name: item.GetObject().GetKey()})
			}
			assertListing(t, got, filterAfter(all, after, ""))
		}
	})

	t.Run("delete markers are not listed", func(t *testing.T) {
		res := f.h.listObjects("dir00/sub00/", "", "", maxReadLimit)
		for _, item := range res.GetItems() {
			if item.GetObject().GetKey() == "dir00/sub00/obj004" {
				t.Fatal("deleted key listed")
			}
		}
	})

	t.Run("a common prefix is emitted once", func(t *testing.T) {
		n := 0
		for _, item := range f.h.listObjects("", "/", "", maxReadLimit).GetItems() {
			if item.GetCommonPrefix() == "dir00/" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("dir00/ emitted %d times, want once", n)
		}
	})

	t.Run("zero limit is rejected", func(t *testing.T) {
		_, err := f.h.read(&pb.ReadQuery{Query: &pb.ReadQuery_ListObjects{ListObjects: &pb.ListObjectsQuery{
			BucketId: uint64(testBucket),
		}}})
		if err == nil {
			t.Fatal("limit 0: want error")
		}
	})
}

// uploadFixture is a bucket with many uploads: several uploads per key, some
// keys sharing a prefix, and keys with special bytes.
type uploadFixture struct {
	h       *harness
	uploads []listed // name is the key, id the upload id
}

func buildUploadFixture(t *testing.T) *uploadFixture {
	t.Helper()
	h := newHarness(t, KindData)
	f := &uploadFixture{h: h}
	add := func(key string, n int) {
		for j := 0; j < n; j++ {
			id := fmt.Sprintf("u-%s-%d", strings.ReplaceAll(key, "/", "_"), j)
			h.mustApply(createUploadCmd(testBucket, key, id))
			if j%2 == 0 {
				h.mustApply(putPartCmd(testBucket, key, id, 1, "p", partData(1)))
			}
			f.uploads = append(f.uploads, listed{name: key, id: id})
		}
	}
	for i := 0; i < 40; i++ {
		key := fmt.Sprintf("p%d/q%02d", i%4, i)
		if i%5 == 0 {
			key = fmt.Sprintf("root%02d", i)
		}
		add(key, 1+i%3)
	}
	add("p1/", 2)
	add("k\x02", 1)
	add("k\x01", 2)
	add("é/ü", 1)
	return f
}

// fetch returns a ListUploads page function for pages.
func (f *uploadFixture) fetch(prefix, delim string, limit uint32) func(km, uim string) ([]listed, bool) {
	return func(km, uim string) ([]listed, bool) {
		res := f.h.listUploads(prefix, delim, km, uim, limit)
		out := make([]listed, 0, len(res.GetItems()))
		for _, item := range res.GetItems() {
			if up := item.GetUpload(); up != nil {
				out = append(out, listed{name: up.Key, id: up.UploadId})
			} else {
				out = append(out, listed{name: item.GetCommonPrefix(), prefix: true})
			}
		}
		return out, res.GetExhausted()
	}
}

func TestListUploads(t *testing.T) {
	f := buildUploadFixture(t)

	t.Run("pages equal the model", func(t *testing.T) {
		tests := []struct {
			name          string
			prefix, delim string
			limit         uint32
		}{
			{name: "all uploads, one at a time", limit: 1},
			{name: "all uploads, pages of 6", limit: 6},
			{name: "delimiter, one at a time", delim: "/", limit: 1},
			{name: "delimiter, pages of 4", delim: "/", limit: 4},
			{name: "prefix", prefix: "p1/", delim: "/", limit: 3},
			{name: "NUL and 0x01 keys", prefix: "k", delim: "\x01", limit: 2},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got := pages(t, tc.limit, f.fetch(tc.prefix, tc.delim, tc.limit))
				assertListing(t, got, groupItems(f.uploads, tc.prefix, tc.delim))
			})
		}
	})

	t.Run("key and upload-id markers", func(t *testing.T) {
		// Markers taken from each listing, plus a key marker with an empty
		// upload-id marker. The cursor is the pair (key, id), so the uploads of
		// that key are strictly after it, and the model says so too. The
		// delimited listing also starts from markers inside common prefixes.
		for _, delim := range []string{"", "/"} {
			all := groupItems(f.uploads, "", delim)
			markers := []listed{
				all[0],
				all[len(all)/3],
				all[len(all)/2],
				all[len(all)-1],
				{name: "p2/q06"},
			}
			for _, m := range markers {
				res := f.h.listUploads("", delim, m.name, m.id, maxReadLimit)
				assertListing(t, listedFromUploads(res), filterAfter(all, m.name, m.id))
			}
		}
	})

	t.Run("common prefix at the key marker is not repeated", func(t *testing.T) {
		all := groupItems(f.uploads, "", "/")
		var cp listed
		for _, it := range all {
			if it.prefix && it.name == "p2/" {
				cp = it
			}
		}
		res := f.h.listUploads("", "/", cp.name, "", maxReadLimit)
		for _, item := range res.GetItems() {
			if item.GetCommonPrefix() == "p2/" {
				t.Fatal("common prefix returned again after its own marker")
			}
		}
		assertListing(t, listedFromUploads(res), filterAfter(all, cp.name, ""))
	})
}

// listedFromUploads converts an upload listing page to the model shape.
func listedFromUploads(res *pb.ListUploadsResult) []listed {
	var out []listed
	for _, item := range res.GetItems() {
		if up := item.GetUpload(); up != nil {
			out = append(out, listed{name: up.Key, id: up.UploadId})
		} else {
			out = append(out, listed{name: item.GetCommonPrefix(), prefix: true})
		}
	}
	return out
}

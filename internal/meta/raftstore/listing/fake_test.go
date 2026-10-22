package listing

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
)

// entry is a stored object key, or an in-progress upload when uploadID is set.
type entry struct {
	key      string
	uploadID string
	ts       int64 // upload initiation, unix nanoseconds
}

// objects returns entries for plain keys.
func objects(keys ...string) []entry {
	out := make([]entry, 0, len(keys))
	for _, k := range keys {
		out = append(out, entry{key: k})
	}
	return out
}

// upload returns the entry of an upload of key with the given id.
func upload(key, id string) entry {
	return entry{key: key, uploadID: id}
}

// uploads returns entries for (key, uploadID) pairs.
func uploads(ps ...pair) []entry {
	out := make([]entry, 0, len(ps))
	for _, p := range ps {
		out = append(out, upload(p.key, p.id))
	}
	return out
}

// seq returns fmt.Sprintf(format, i) for i in 0..n-1.
func seq(format string, n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf(format, i))
	}
	return out
}

// listingOf returns the sorted listing one partition's range scan returns for
// entries, with the semantics of fsm/list.go: keys under a common prefix
// collapse into one item sorted by the prefix, and each prefix appears once.
func listingOf(entries []entry, prefix, delim string) []Item {
	seen := make(map[string]bool)
	var items []Item
	for _, e := range entries {
		if !strings.HasPrefix(e.key, prefix) {
			continue
		}
		if delim != "" {
			if i := strings.Index(e.key[len(prefix):], delim); i >= 0 {
				cp := e.key[:len(prefix)+i+len(delim)]
				if !seen[cp] {
					seen[cp] = true
					items = append(items, Item{Sort: cp, CommonPrefix: true})
				}
				continue
			}
		}
		if e.uploadID == "" {
			items = append(items, Item{Sort: e.key, Object: meta.ObjectRecord{Bucket: "b", Key: e.key}})
			continue
		}
		items = append(items, Item{
			Sort:   UploadCursor(e.key, e.uploadID),
			Upload: meta.UploadRecord{Bucket: "b", Key: e.key, UploadID: e.uploadID, Initiated: time.Unix(0, e.ts)},
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Sort < items[j].Sort })
	return items
}

// spread assigns entries to n partitions, numbered from 0, in round-robin
// order, and returns each partition's listing. Shuffle entries first for a
// random assignment.
func spread(entries []entry, n int, prefix, delim string) map[ids.PartitionID][]Item {
	parts := make(map[ids.PartitionID][]Item, n)
	if n == 0 {
		return parts
	}
	subsets := make([][]entry, n)
	for i, e := range entries {
		subsets[i%n] = append(subsets[i%n], e)
	}
	for i, s := range subsets {
		parts[ids.PartitionID(i)] = listingOf(s, prefix, delim)
	}
	return parts
}

// partIDs returns the partition ids 0..n-1.
func partIDs(n int) []ids.PartitionID {
	out := make([]ids.PartitionID, n)
	for i := range out {
		out[i] = ids.PartitionID(i)
	}
	return out
}

// fakeStore answers Fetch from in-memory partition listings, honouring the
// cursor, the limit and the ctx like a partition's range scan does.
type fakeStore struct {
	parts map[ids.PartitionID][]Item
	// inject, when set, runs on every fetch after it is counted, with the
	// partition and its 1-based call number. A non-nil result fails the fetch.
	// It may cancel the caller's context.
	inject func(p ids.PartitionID, call int) error

	mu      sync.Mutex
	calls   map[ids.PartitionID]int
	limits  []int
	cursors map[ids.PartitionID][]string
}

func newFakeStore(parts map[ids.PartitionID][]Item) *fakeStore {
	return &fakeStore{
		parts:   parts,
		calls:   make(map[ids.PartitionID]int),
		cursors: make(map[ids.PartitionID][]string),
	}
}

func (s *fakeStore) fetch(ctx context.Context, p ids.PartitionID, cursor string, limit int) (Batch, error) {
	s.mu.Lock()
	s.calls[p]++
	call := s.calls[p]
	s.limits = append(s.limits, limit)
	s.cursors[p] = append(s.cursors[p], cursor)
	s.mu.Unlock()

	if s.inject != nil {
		if err := s.inject(p, call); err != nil {
			return Batch{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Batch{}, err
	}
	items := s.parts[p]
	i := sort.Search(len(items), func(i int) bool { return items[i].Sort > cursor })
	end := min(i+limit, len(items))
	return Batch{Items: items[i:end], Exhausted: end == len(items)}, nil
}

// callCount returns how many fetches partition p received.
func (s *fakeStore) callCount(p ids.PartitionID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[p]
}

// limitsSeen returns the limit of every fetch so far.
func (s *fakeStore) limitsSeen() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.limits...)
}

// firstCursor returns the cursor of partition p's first fetch, or "" + false.
func (s *fakeStore) firstCursor(p ids.PartitionID) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cursors[p]) == 0 {
		return "", false
	}
	return s.cursors[p][0], true
}

// row is one listing entry in comparable form: an object key or a common prefix.
type row struct {
	sort string
	cp   bool
}

// rowsOf returns the rows of a sorted listing.
func rowsOf(items []Item) []row {
	out := make([]row, 0, len(items))
	for _, it := range items {
		out = append(out, row{it.Sort, it.CommonPrefix})
	}
	return out
}

// objectPageRows returns the entries of a page, sorted.
func objectPageRows(res meta.ListResult) []row {
	var out []row
	for _, o := range res.Objects {
		out = append(out, row{o.Key, false})
	}
	for _, p := range res.CommonPrefixes {
		out = append(out, row{p, true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].sort < out[j].sort })
	return out
}

// uploadPageRows returns the entries of an upload page, sorted.
func uploadPageRows(res object.ListUploadsResult) []row {
	var out []row
	for _, u := range res.Uploads {
		out = append(out, row{UploadCursor(u.Key, u.UploadID), false})
	}
	for _, p := range res.CommonPrefixes {
		out = append(out, row{p, true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].sort < out[j].sort })
	return out
}

// pageAfter returns the first n oracle rows sorting after cursor, and whether
// more rows follow them. The empty cursor means the start.
func pageAfter(oracle []row, cursor string, n int) ([]row, bool) {
	var rest []row
	for _, r := range oracle {
		if cursor == "" || r.sort > cursor {
			rest = append(rest, r)
		}
	}
	if len(rest) > n {
		return rest[:n], true
	}
	return rest, false
}

// pageSizes returns the page sizes the paging tests exercise for a listing of
// total entries: small sizes, and sizes around the total.
func pageSizes(total int) []int {
	var out []int
	for _, n := range []int{1, 2, 3, 7, total - 1, total, total + 1} {
		if n >= 1 && n <= object.MaxListKeys {
			out = append(out, n)
		}
	}
	return out
}

// randomEntries returns n distinct random entries over a small alphabet that
// includes "/", 0x01 and a multi-byte rune, so that prefixes, delimiters and
// bytewise ordering all come into play. With withUploads, each entry is an
// upload with one of three ids.
func randomEntries(rng *rand.Rand, n int, withUploads bool) []entry {
	alphabet := []string{"a", "b", "/", "\x01", "é", "z"}
	seen := make(map[string]bool)
	var out []entry
	for len(out) < n {
		var sb strings.Builder
		for range 1 + rng.IntN(4) {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		e := entry{key: sb.String(), ts: int64(len(out)) + 1}
		if withUploads {
			e.uploadID = []string{"1", "2", "9"}[rng.IntN(3)]
		}
		if seen[e.key+"\x00"+e.uploadID] {
			continue
		}
		seen[e.key+"\x00"+e.uploadID] = true
		out = append(out, e)
	}
	return out
}

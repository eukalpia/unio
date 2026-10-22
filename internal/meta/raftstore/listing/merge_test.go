package listing

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
)

// TestLazyRefill checks that a partition is refetched only when its head is
// needed. A page that stops inside the first batch never refetches it; a
// full page refetches it only when the lookahead needs the next head.
func TestLazyRefill(t *testing.T) {
	big := seq("a%03d", 300)
	tests := []struct {
		name    string
		maxKeys int
		want0   int // fetches of partition 0, which holds big
	}{
		{name: "page inside the first batch", maxKeys: 200, want0: 1},
		{name: "lookahead inside the first batch", maxKeys: 255, want0: 1},
		{name: "lookahead needs the next batch", maxKeys: 256, want0: 2},
		{name: "page refetches inside the page", maxKeys: 300, want0: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore(map[ids.PartitionID][]Item{
				0: listingOf(objects(big...), "", ""),
				1: listingOf(objects("z"), "", ""),
			})
			if _, err := ListObjects(context.Background(), partIDs(2), store.fetch, object.ListOptions{MaxKeys: tt.maxKeys}); err != nil {
				t.Fatalf("ListObjects: %v", err)
			}
			if got := store.callCount(0); got != tt.want0 {
				t.Errorf("partition 0 fetches = %d, want %d", got, tt.want0)
			}
			if got := store.callCount(1); got != 1 {
				t.Errorf("partition 1 fetches = %d, want 1", got)
			}
		})
	}
}

// TestFarPartitionFetchedOnce runs 4 partitions with interleaved keys in the
// first three and keys that all sort after the page in the fourth. With
// MaxKeys 5 no partition is read beyond its first batch.
func TestFarPartitionFetchedOnce(t *testing.T) {
	near := spread(objects(seq("k%03d", 30)...), 3, "", "")
	parts := map[ids.PartitionID][]Item{
		0: near[0],
		1: near[1],
		2: near[2],
		3: listingOf(objects(seq("z%02d", 10)...), "", ""),
	}
	store := newFakeStore(parts)
	res, err := ListObjects(context.Background(), partIDs(4), store.fetch, object.ListOptions{MaxKeys: 5})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if got := objectKeys(res.Objects); strings.Join(got, ",") != "k000,k001,k002,k003,k004" {
		t.Fatalf("objects = %q", got)
	}
	if !res.IsTruncated {
		t.Errorf("IsTruncated = false, want true")
	}
	for p := range ids.PartitionID(4) {
		if got := store.callCount(p); got != 1 {
			t.Errorf("partition %d fetches = %d, want 1", p, got)
		}
	}
}

// TestFetchLimit checks the batch limit of every fetch: min(MaxKeys+1, 256),
// where zero MaxKeys means the maximum page size.
func TestFetchLimit(t *testing.T) {
	tests := []struct {
		maxKeys int
		want    int
	}{
		{0, 256},
		{1, 2},
		{5, 6},
		{255, 256},
		{256, 256},
		{object.MaxListKeys, 256},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("maxKeys=%d", tt.maxKeys), func(t *testing.T) {
			store := newFakeStore(spread(objects(seq("k%03d", 40)...), 3, "", ""))
			if _, err := ListObjects(context.Background(), partIDs(3), store.fetch, object.ListOptions{MaxKeys: tt.maxKeys}); err != nil {
				t.Fatalf("ListObjects: %v", err)
			}
			limits := store.limitsSeen()
			if len(limits) == 0 {
				t.Fatal("no fetches")
			}
			for _, l := range limits {
				if l != tt.want {
					t.Fatalf("limit = %d, want %d", l, tt.want)
				}
			}
		})
	}
}

// TestBrokenPartitions checks that a partition violating the Fetch contract
// fails the listing instead of producing a wrong page or looping.
func TestBrokenPartitions(t *testing.T) {
	tests := []struct {
		name    string
		fetch   Fetch
		maxKeys int
		wantErr string
	}{
		{
			name: "items out of order",
			fetch: func(context.Context, ids.PartitionID, string, int) (Batch, error) {
				return Batch{Items: []Item{{Sort: "b"}, {Sort: "a"}}, Exhausted: true}, nil
			},
			maxKeys: 5,
			wantErr: "out of order",
		},
		{
			name: "same item again after a refill",
			fetch: func(_ context.Context, _ ids.PartitionID, cursor string, _ int) (Batch, error) {
				if cursor == "" {
					return Batch{Items: []Item{{Sort: "a"}}}, nil
				}
				return Batch{Items: []Item{{Sort: "a"}}, Exhausted: true}, nil
			},
			maxKeys: 5,
			wantErr: "out of order",
		},
		{
			name: "empty batch that is not exhausted",
			fetch: func(context.Context, ids.PartitionID, string, int) (Batch, error) {
				return Batch{}, nil
			},
			maxKeys: 5,
			wantErr: "empty batch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ListObjects(context.Background(), partIDs(1), tt.fetch, object.ListOptions{MaxKeys: tt.maxKeys})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestUploadCursorOrder checks that cursors sort in (key, uploadID) order,
// and that a common prefix P sorts before the uploads under it and after the
// uploads of keys below it, so one ordering serves both kinds of item.
func TestUploadCursorOrder(t *testing.T) {
	keys := []string{"a", "a\x01", "a\x01b", "a\x02", "a/", "a/b", "a0", "b", "é"}
	uploadIDs := []string{"1", "2", "10", "z"}
	for _, k1 := range keys {
		for _, i1 := range uploadIDs {
			for _, k2 := range keys {
				for _, i2 := range uploadIDs {
					tupleLess := k1 < k2 || (k1 == k2 && i1 < i2)
					cursorLess := UploadCursor(k1, i1) < UploadCursor(k2, i2)
					if tupleLess != cursorLess {
						t.Fatalf("(%q,%q) < (%q,%q): tuple %v, cursor %v", k1, i1, k2, i2, tupleLess, cursorLess)
					}
				}
			}
			for _, p := range keys {
				if got, want := UploadCursor(k1, i1) < p, k1 < p; got != want {
					t.Fatalf("cursor(%q,%q) < prefix %q: got %v, want %v", k1, i1, p, got, want)
				}
			}
		}
	}
}

func TestSplitUploadCursor(t *testing.T) {
	tests := []struct {
		cursor, wantKey, wantID string
	}{
		{"", "", ""},
		{"a", "a", ""},
		{"a/", "a/", ""},
		{"a\x00", "a", ""},
		{"a\x001", "a", "1"},
		{"a\x01\x00b", "a\x01", "b"},
	}
	for _, tt := range tests {
		key, id := SplitUploadCursor(tt.cursor)
		if key != tt.wantKey || id != tt.wantID {
			t.Errorf("SplitUploadCursor(%q) = (%q, %q), want (%q, %q)", tt.cursor, key, id, tt.wantKey, tt.wantID)
		}
	}
}

// TestUploadCursorRoundTrip checks that SplitUploadCursor inverts UploadCursor
// for random keys (which never contain NUL) and ids.
func TestUploadCursorRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 9))
	for _, e := range randomEntries(rng, 200, true) {
		key, id := SplitUploadCursor(UploadCursor(e.key, e.uploadID))
		if key != e.key || id != e.uploadID {
			t.Fatalf("round trip of (%q, %q) = (%q, %q)", e.key, e.uploadID, key, id)
		}
	}
}

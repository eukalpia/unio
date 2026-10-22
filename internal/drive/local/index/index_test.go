package index

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func testID(n int) ids.ShardID {
	var s ids.ShardID
	s.Data[0] = byte(n)
	s.Data[15] = byte(n >> 8)
	s.Index = uint16(n)
	return s
}

func openIndex(t *testing.T) (*Index, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "index")
	x, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = x.Close() })
	return x, dir
}

func commit(t *testing.T, b *Batch) {
	t.Helper()
	if err := b.Commit(true); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestLocationRoundTrip(t *testing.T) {
	cases := []Location{
		{Kind: KindPacked, Segment: 1, Offset: 0, Length: 0, Created: 1},
		{Kind: KindPacked, Segment: 1<<63 + 3, Offset: 1 << 40, Length: 1 << 20, Created: -5},
		{Kind: KindBlob, Length: 8 << 20, Created: 123456789},
	}
	for _, loc := range cases {
		got, err := decodeLocation(encodeLocation(loc))
		if err != nil || !reflect.DeepEqual(got, loc) {
			t.Fatalf("round trip %+v: got %+v, %v", loc, got, err)
		}
	}
}

func TestDecodeLocationRejectsBadInput(t *testing.T) {
	good := encodeLocation(Location{Kind: KindPacked, Segment: 2})
	for _, b := range [][]byte{nil, good[:10], append(append([]byte(nil), good...), 0), {9, 0, 0}} {
		if _, err := decodeLocation(b); err == nil {
			t.Fatalf("decodeLocation(%x) accepted bad input", b)
		}
	}
}

func TestPutGetDelete(t *testing.T) {
	x, _ := openIndex(t)
	id := testID(1)
	loc := Location{Kind: KindPacked, Segment: 3, Offset: 42, Length: 100, Created: 7}

	if _, err := x.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: err = %v", err)
	}
	b := x.Batch()
	if err := b.PutShard(id, loc); err != nil {
		t.Fatal(err)
	}
	commit(t, b)
	got, err := x.Get(id)
	if err != nil || got != loc {
		t.Fatalf("get: %+v, %v; want %+v", got, err, loc)
	}

	b = x.Batch()
	if err := b.DeleteShard(id); err != nil {
		t.Fatal(err)
	}
	commit(t, b)
	if _, err := x.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: err = %v", err)
	}
}

func TestBatchOrderWithinOneCommit(t *testing.T) {
	x, _ := openIndex(t)
	id := testID(2)
	b := x.Batch()
	if err := b.PutShard(id, Location{Kind: KindBlob, Length: 1}); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteShard(id); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 2 {
		t.Fatalf("len = %d", b.Len())
	}
	commit(t, b)
	if _, err := x.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("put then delete in one batch should leave nothing: %v", err)
	}
}

func TestWalkAndSegmentsAreSeparate(t *testing.T) {
	x, _ := openIndex(t)
	b := x.Batch()
	for i := 1; i <= 3; i++ {
		if err := b.PutShard(testID(i), Location{Kind: KindPacked, Segment: uint64(i), Length: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.PutSegment(1, SegmentMeta{End: 100, Sealed: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.PutSegment(2, SegmentMeta{End: 5}); err != nil {
		t.Fatal(err)
	}
	commit(t, b)

	var seen []ids.ShardID
	if err := x.Walk(func(id ids.ShardID, loc Location) error {
		if loc.Length != int64(id.Index) {
			t.Fatalf("walk: %s has length %d", id, loc.Length)
		}
		seen = append(seen, id)
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("walk saw %d shards, want 3", len(seen))
	}

	segs, err := x.Segments()
	if err != nil {
		t.Fatalf("segments: %v", err)
	}
	want := map[uint64]SegmentMeta{1: {End: 100, Sealed: true}, 2: {End: 5}}
	if !reflect.DeepEqual(segs, want) {
		t.Fatalf("segments = %+v, want %+v", segs, want)
	}

	b = x.Batch()
	if err := b.DeleteSegment(1); err != nil {
		t.Fatal(err)
	}
	commit(t, b)
	segs, _ = x.Segments()
	if _, ok := segs[1]; ok || len(segs) != 1 {
		t.Fatalf("segment 1 not deleted: %+v", segs)
	}
}

func TestWalkStopsOnCallbackError(t *testing.T) {
	x, _ := openIndex(t)
	b := x.Batch()
	for i := 1; i <= 5; i++ {
		_ = b.PutShard(testID(i), Location{Kind: KindBlob})
	}
	commit(t, b)
	stop := errors.New("stop")
	calls := 0
	err := x.Walk(func(ids.ShardID, Location) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("walk: err %v after %d calls", err, calls)
	}
}

func TestReopenKeepsCommittedData(t *testing.T) {
	x, dir := openIndex(t)
	loc := Location{Kind: KindBlob, Length: 9, Created: 1}
	b := x.Batch()
	if err := b.PutShard(testID(4), loc); err != nil {
		t.Fatal(err)
	}
	commit(t, b)
	if err := x.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	y, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer y.Close()
	got, err := y.Get(testID(4))
	if err != nil || got != loc {
		t.Fatalf("after reopen: %+v, %v", got, err)
	}
}

func TestUncommittedBatchIsNotApplied(t *testing.T) {
	x, _ := openIndex(t)
	b := x.Batch()
	if err := b.PutShard(testID(5), Location{Kind: KindBlob}); err != nil {
		t.Fatal(err)
	}
	if err := b.Discard(); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := x.Get(testID(5)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("discarded batch applied: %v", err)
	}
}

func TestWalkPagesAcrossManyShards(t *testing.T) {
	x, _ := openIndex(t)
	const n = 1300 // more than two pages
	b := x.Batch()
	for i := 1; i <= n; i++ {
		if err := b.PutShard(testID(i), Location{Kind: KindBlob, Length: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	commit(t, b)

	count := 0
	seen := make(map[ids.ShardID]bool, n)
	if err := x.Walk(func(id ids.ShardID, loc Location) error {
		count++
		if seen[id] {
			t.Fatalf("shard %s visited twice", id)
		}
		seen[id] = true
		if loc.Length != int64(id.Index) {
			t.Fatalf("shard %s has length %d", id, loc.Length)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if count != n {
		t.Fatalf("walk visited %d shards, want %d", count, n)
	}
}

func TestPageStartsAtOrAfterStart(t *testing.T) {
	x, _ := openIndex(t)
	b := x.Batch()
	for i := 1; i <= 5; i++ {
		_ = b.PutShard(testID(i), Location{Kind: KindBlob})
	}
	commit(t, b)
	page, err := x.Page(testID(3), 10)
	if err != nil || len(page) != 3 || page[0].ID != testID(3) {
		t.Fatalf("page from 3: %d entries, first %v, err %v", len(page), page[0].ID, err)
	}
}

func TestSuccessorBoundaries(t *testing.T) {
	var last ids.ShardID
	for i := range last.Data {
		last.Data[i] = 0xff
	}
	last.Index = 0xffff
	if _, ok := successor(last); ok {
		t.Fatal("successor of the maximum id must not exist")
	}
	// Index 0xffff carries into the last byte of Data, the least significant
	// part of the big-endian key.
	next, ok := successor(ids.ShardID{Index: 0xffff})
	if !ok || next.Index != 0 || next.Data[15] != 1 {
		t.Fatalf("successor of index 0xffff = %+v, %v", next, ok)
	}
}

// FuzzDecodeLocation checks that arbitrary index values never panic the
// decoder and that accepted values re-encode to the same bytes.
func FuzzDecodeLocation(f *testing.F) {
	f.Add(encodeLocation(Location{Kind: KindPacked, Segment: 1, Offset: 2, Length: 3, Created: 4}))
	f.Add(encodeLocation(Location{Kind: KindBlob, Length: 1 << 30}))
	f.Fuzz(func(t *testing.T, b []byte) {
		loc, err := decodeLocation(b)
		if err != nil {
			return
		}
		if got := encodeLocation(loc); string(got) != string(b) {
			t.Fatalf("accepted value does not round-trip")
		}
	})
}

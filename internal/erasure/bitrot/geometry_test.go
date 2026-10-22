package bitrot

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

func TestShardBlockSize(t *testing.T) {
	tests := []struct {
		name      string
		stripe, k int
		wantBlock int
	}{
		{"exact", 1 << 20, 2, 1 << 19},
		{"round up", 1 << 20, 3, 349526},
		{"small", 10, 4, 3},
		{"one byte", 1, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShardBlockSize(tt.stripe, tt.k); got != tt.wantBlock {
				t.Fatalf("ShardBlockSize(%d,%d) = %d, want %d", tt.stripe, tt.k, got, tt.wantBlock)
			}
		})
	}
}

func TestShardSize(t *testing.T) {
	// Stripe 10 with k=4 gives full blocks of 3 bytes; a short final stripe
	// of L bytes gives a block of ceil(L/4) bytes.
	tests := []struct {
		name string
		size int64
		k    int
		s    int
		want int64
	}{
		{"empty", 0, 4, 10, 0},
		{"one byte", 1, 2, 1 << 20, 16 + 1},
		{"exact stripe", 1 << 20, 2, 1 << 20, 16 + 1<<19},
		{"stripe plus one", 1<<20 + 1, 2, 1 << 20, 16 + 1<<19 + 16 + 1},
		{"three stripes", 25, 4, 10, 19 + 19 + 18},
		{"ten stripes plus seven", 10*(1<<20) + 7, 4, 1 << 20, 10*(16+1<<18) + 16 + 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShardSize(tt.size, tt.k, tt.s); got != tt.want {
				t.Fatalf("ShardSize(%d,%d,%d) = %d, want %d", tt.size, tt.k, tt.s, got, tt.want)
			}
		})
	}
}

func TestStripeAndBlockLen(t *testing.T) {
	const size, stripe, k = 25, 10, 4
	if n := Stripes(size, stripe); n != 3 {
		t.Fatalf("Stripes = %d, want 3", n)
	}
	wantStripe := []int64{10, 10, 5}
	wantBlock := []int{3, 3, 2}
	for s := range int64(3) {
		if got := StripeLen(size, stripe, s); got != wantStripe[s] {
			t.Fatalf("StripeLen(%d) = %d, want %d", s, got, wantStripe[s])
		}
		if got := BlockLen(size, k, stripe, s); got != wantBlock[s] {
			t.Fatalf("BlockLen(%d) = %d, want %d", s, got, wantBlock[s])
		}
	}
	if n := Stripes(0, stripe); n != 0 {
		t.Fatalf("Stripes(0) = %d, want 0", n)
	}
}

func TestSpan(t *testing.T) {
	// Object of 25 bytes, k=4, stripe 10. Framed blocks: 19, 19, 18 bytes.
	const size, k, stripe = 25, 4, 10
	tests := []struct {
		name                     string
		offset, length           int64
		first, end               int64
		shardOff, shardLen, skip int64
	}{
		{"whole object", 0, 25, 0, 3, 0, 56, 0},
		{"crosses boundary", 9, 2, 0, 2, 0, 38, 9},
		{"first stripe only", 0, 10, 0, 1, 0, 19, 0},
		{"second stripe exactly", 10, 10, 1, 2, 19, 19, 0},
		{"short last stripe", 20, 5, 2, 3, 38, 18, 0},
		{"inside last stripe", 24, 1, 2, 3, 38, 18, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp, err := Span(size, k, stripe, tt.offset, tt.length)
			if err != nil {
				t.Fatalf("Span: %v", err)
			}
			want := Range{
				FirstStripe: tt.first,
				EndStripe:   tt.end,
				Shard:       Extent{Offset: tt.shardOff, Length: tt.shardLen, FirstBlock: tt.first},
				Skip:        tt.skip,
			}
			if sp != want {
				t.Fatalf("Span = %+v, want %+v", sp, want)
			}
		})
	}
}

func TestSpanOfEmptyRangeIsZero(t *testing.T) {
	for _, tc := range []struct{ size, offset int64 }{{25, 7}, {25, 0}, {0, 0}, {25, 25}} {
		sp, err := Span(tc.size, 4, 10, tc.offset, 0)
		if err != nil {
			t.Fatalf("Span(%d, %d, 0): %v", tc.size, tc.offset, err)
		}
		if sp != (Range{}) {
			t.Fatalf("Span of an empty range = %+v, want zero", sp)
		}
	}
}

func TestSpanRejectsBadArguments(t *testing.T) {
	tests := []struct {
		name           string
		size           int64
		k, stripe      int
		offset, length int64
	}{
		{"negative size", -1, 2, 10, 0, 0},
		{"zero shards", 10, 0, 10, 0, 1},
		{"zero stripe", 10, 2, 0, 0, 1},
		{"negative offset", 10, 2, 10, -1, 1},
		{"negative length", 10, 2, 10, 0, -1},
		{"past end", 10, 2, 10, 5, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Span(tt.size, tt.k, tt.stripe, tt.offset, tt.length); !errors.Is(err, ErrRange) {
				t.Fatalf("Span: err = %v, want ErrRange", err)
			}
		})
	}
}

func TestStripeExtent(t *testing.T) {
	// Framed blocks of an object of 25 bytes, k=4, stripe 10: 19, 19, 18 bytes.
	const size, k, stripe = 25, 4, 10
	tests := []struct {
		name             string
		first, end       int64
		wantOff, wantLen int64
	}{
		{"all stripes", 0, 3, 0, 56},
		{"middle stripe", 1, 2, 19, 19},
		{"last stripe", 2, 3, 38, 18},
		{"first two", 0, 2, 0, 38},
		{"last two", 1, 3, 19, 37},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripeExtent(size, k, stripe, tt.first, tt.end)
			want := Extent{Offset: tt.wantOff, Length: tt.wantLen, FirstBlock: tt.first}
			if got != want {
				t.Fatalf("StripeExtent = %+v, want %+v", got, want)
			}
		})
	}
}

// TestRangeReadsVerifyAtDerivedPositions reads windows of stripes the way
// a range read does: the extent of the window is cut from a fully written
// shard, and a reader built from the extent's position must verify exactly
// the blocks of the window. A reader one block off must fail.
func TestRangeReadsVerifyAtDerivedPositions(t *testing.T) {
	const size, k, stripe = 25, 4, 10
	id := ids.ShardID{Data: ids.DataID{7, 7}, Index: 2}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*31 + 5)
	}
	// Write the whole shard: block s holds the bytes of stripe s, padded.
	var shard bytes.Buffer
	w := NewWriter(&shard, Position{Data: id.Data, Shard: id.Index})
	for s := range Stripes(size, stripe) {
		block := make([]byte, BlockLen(size, k, stripe, s))
		copy(block, data[s*stripe:s*stripe+StripeLen(size, stripe, s)])
		if err := w.WriteBlock(block); err != nil {
			t.Fatal(err)
		}
	}
	shardBytes := shard.Bytes()
	if int64(len(shardBytes)) != ShardSize(size, k, stripe) {
		t.Fatalf("shard is %d bytes, want %d", len(shardBytes), ShardSize(size, k, stripe))
	}

	windows := []struct{ offset, length int64 }{
		{0, 1}, {9, 2}, {10, 10}, {20, 5}, {24, 1}, {0, 25}, {9, 16},
	}
	for _, win := range windows {
		sp, err := Span(size, k, stripe, win.offset, win.length)
		if err != nil {
			t.Fatal(err)
		}
		window := shardBytes[sp.Shard.Offset : sp.Shard.Offset+sp.Shard.Length]
		// Verify every block of the window from its own position.
		r := NewReader(bytes.NewReader(window), sp.Shard.Position(id))
		for s := sp.FirstStripe; s < sp.EndStripe; s++ {
			if err := r.ReadBlock(make([]byte, BlockLen(size, k, stripe, s))); err != nil {
				t.Fatalf("window [%d,+%d) stripe %d: %v", win.offset, win.length, s, err)
			}
		}
		if err := r.Done(); err != nil {
			t.Fatalf("window [%d,+%d): %v", win.offset, win.length, err)
		}
		// Off by one block, the same window must fail on its first block.
		off := NewReader(bytes.NewReader(window), Position{Data: id.Data, Shard: id.Index, FirstBlock: uint64(sp.FirstStripe) + 1})
		if err := off.ReadBlock(make([]byte, BlockLen(size, k, stripe, sp.FirstStripe))); !errors.Is(err, erasure.ErrCorrupt) {
			t.Fatalf("window [%d,+%d) read one block off: err = %v, want corrupt", win.offset, win.length, err)
		}
	}
}

package bitrot

import "testing"

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
		{"empty range", 7, 0, 0, 0, 0, 0, 0},
		{"empty object", 0, 0, 0, 0, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sz := int64(size)
			if tt.name == "empty object" {
				sz = 0
			}
			sp, err := Span(sz, k, stripe, tt.offset, tt.length)
			if err != nil {
				t.Fatalf("Span: %v", err)
			}
			want := Range{FirstStripe: tt.first, EndStripe: tt.end, ShardOffset: tt.shardOff, ShardLength: tt.shardLen, Skip: tt.skip}
			if sp != want {
				t.Fatalf("Span = %+v, want %+v", sp, want)
			}
		})
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
			if _, err := Span(tt.size, tt.k, tt.stripe, tt.offset, tt.length); err == nil {
				t.Fatal("Span accepted invalid arguments")
			}
		})
	}
}

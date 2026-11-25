package local

import (
	"context"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// TestForDataPicksTheSmallestClassThatHoldsTheRecord checks the class of each
// packed record size, including the edges of the classes.
func TestForDataPicksTheSmallestClassThatHoldsTheRecord(t *testing.T) {
	r, err := Options{}.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	p := newPools(r)
	h := int64(segment.HeaderSize)
	cases := []struct {
		name string
		data int64
		want int64 // size of the pool
	}{
		{"empty shard", 0, 64<<10 + h},
		{"record fills the smallest class", 64 << 10, 64<<10 + h},
		{"record one byte over the smallest class", 64<<10 + 1, 128<<10 + h},
		{"shard of a 1 MiB object on 16 drives", 87382, 128<<10 + h},
		{"record in the 256 KiB class", 200 << 10, 256<<10 + h},
		{"record in the 512 KiB class", 400 << 10, 512<<10 + h},
		{"record needs the largest class", 600 << 10, 1<<20 + h},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := int64(p.forData(c.data).Size()); got != c.want {
				t.Fatalf("forData(%d).Size() = %d, want %d", c.data, got, c.want)
			}
		})
	}
}

// TestPackedShardHoldsTheBufferOfItsClass holds an erasure-sized shard
// mid-write and checks that the budget charges the class that holds it, not
// the buffer of the largest packed record.
func TestPackedShardHoldsTheBufferOfItsClass(t *testing.T) {
	const shard = 87382
	budget := bufpool.NewBudget(64 << 20)
	d := openOnBudget(t, budget)

	var id ids.ShardID
	id.Data[0] = 9
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- d.WriteShard(context.Background(), id, pr, shard) }()
	if _, err := pw.Write(make([]byte, shard/2)); err != nil {
		t.Fatalf("feed first half: %v", err)
	}
	want := int64(128<<10 + segment.HeaderSize)
	if got := budget.InUse(); got != want {
		t.Fatalf("InUse with the shard held = %d, want %d", got, want)
	}
	if _, err := pw.Write(make([]byte, shard-shard/2)); err != nil {
		t.Fatalf("feed second half: %v", err)
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("close payload: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("write shard: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close drive: %v", err)
	}
}

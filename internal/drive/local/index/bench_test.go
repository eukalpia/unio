package index

import (
	"path/filepath"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

// BenchmarkGet measures point lookups in an index of 100k shards, with the
// working set larger than a typical block cache.
func BenchmarkGet(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "index")
	x, err := Open(dir, nil)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = x.Close() })

	const population = 100_000
	batch := x.Batch()
	for i := 0; i < population; i++ {
		if err := batch.PutShard(benchShard(i), Location{Kind: KindPacked, Segment: 1, Offset: int64(i), Length: 64}); err != nil {
			b.Fatal(err)
		}
		if (i+1)%4096 == 0 {
			if err := batch.Commit(false); err != nil {
				b.Fatal(err)
			}
			batch = x.Batch()
		}
	}
	if err := batch.Commit(true); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := x.Get(benchShard((i * 7919) % population)); err != nil {
			b.Fatalf("get: %v", err)
		}
	}
}

func benchShard(i int) ids.ShardID {
	var id ids.ShardID
	id.Data[0] = byte(i >> 16)
	id.Data[1] = byte(i >> 8)
	id.Data[2] = byte(i)
	id.Data[15] = 0xbe
	return id
}

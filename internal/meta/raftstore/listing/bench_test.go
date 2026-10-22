package listing

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
)

// BenchmarkListObjectsMerge measures the merge alone: 16 partitions of 1000
// keys each, merged into one page of 1000 keys. The fetch is an in-memory
// binary search over prebuilt listings, so no I/O is measured.
func BenchmarkListObjectsMerge(b *testing.B) {
	const partitions = 16
	entries := make([]entry, 0, partitions*1000)
	for i := range partitions * 1000 {
		entries = append(entries, entry{key: fmt.Sprintf("k%06d", i)})
	}
	lists := spread(entries, partitions, "", "")
	fetch := func(_ context.Context, p ids.PartitionID, cursor string, limit int) (Batch, error) {
		items := lists[p]
		i := sort.Search(len(items), func(i int) bool { return items[i].Sort > cursor })
		end := min(i+limit, len(items))
		return Batch{Items: items[i:end], Exhausted: end == len(items)}, nil
	}
	parts := partIDs(partitions)
	opts := object.ListOptions{MaxKeys: 1000}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		res, err := ListObjects(ctx, parts, fetch, opts)
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Objects) != 1000 {
			b.Fatalf("got %d objects, want 1000", len(res.Objects))
		}
	}
}

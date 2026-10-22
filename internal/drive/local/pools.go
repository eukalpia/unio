package local

import (
	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// blobAlign is the alignment of blob buffers, which O_DIRECT requires.
const blobAlign int64 = 4096

// packedClasses are the data sizes of the packed record pools, below the
// largest record. A packed record takes the smallest class that holds it, so
// an erasure shard of a few hundred kilobytes does not hold a buffer sized for
// the largest record. The largest record has its own pool, whatever the
// packed size limit, so every record fits some class.
var packedClasses = []int64{smallRecordLimit, 128 << 10, 256 << 10, 512 << 10}

// pools holds the I/O buffer pools of one drive. They draw from one budget,
// the shared Options.Budget when the caller gives one, which bounds total
// buffer memory (architecture section 11).
type pools struct {
	packed []*bufpool.Pool // packed records by size, ascending; the last holds the largest
	small  *bufpool.Pool   // the smallest packed class, which compaction reads through
	blob   *bufpool.Pool   // blob writes, aligned for O_DIRECT
}

// newPools creates the pools for resolved options. The options were validated
// so that every pool fits in the budget.
func newPools(r resolved) pools {
	budget := r.Budget
	if budget == nil {
		budget = bufpool.NewBudget(r.budget)
	}
	var packed []*bufpool.Pool
	for _, limit := range packedClasses {
		if limit >= r.packedMax {
			break
		}
		packed = append(packed, bufpool.NewPool(int(limit+segment.HeaderSize), budget))
	}
	packed = append(packed, bufpool.NewPool(int(r.bigPoolSize()), budget))
	return pools{
		packed: packed,
		small:  packed[0],
		blob:   bufpool.NewPool(int(blobChunk), budget, bufpool.WithAlignment(int(blobAlign))),
	}
}

// forData returns the pool for a packed record whose data is n bytes: the
// smallest class that holds the record.
func (p pools) forData(n int64) *bufpool.Pool {
	need := segment.RecordSize(n)
	for _, c := range p.packed {
		if need <= int64(c.Size()) {
			return c
		}
	}
	return p.packed[len(p.packed)-1]
}

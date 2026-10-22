package local

import (
	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// pools holds the I/O buffer pools of one drive. They draw from one budget,
// which bounds total buffer memory (architecture section 11).
type pools struct {
	small *bufpool.Pool // packed records up to smallRecordLimit
	big   *bufpool.Pool // the largest packed records
	blob  *bufpool.Pool // blob writes, aligned for O_DIRECT
}

// newPools creates the pools for resolved options. The options were validated
// so that every pool fits in the budget.
func newPools(r resolved) pools {
	budget := bufpool.NewBudget(r.budget)
	return pools{
		small: bufpool.NewPool(int(r.smallPoolSize()), budget),
		big:   bufpool.NewPool(int(r.bigPoolSize()), budget),
		blob:  bufpool.NewPool(int(blobChunk), budget, bufpool.WithAlignment(4096)),
	}
}

// forData returns the pool for a packed record whose data is n bytes.
func (p pools) forData(n int64) *bufpool.Pool {
	if segment.RecordSize(n) <= int64(p.small.Size()) {
		return p.small
	}
	return p.big
}

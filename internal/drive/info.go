package drive

import (
	"time"

	"github.com/eukalpia/unio/internal/ids"
)

// ShardInfo describes a stored shard.
type ShardInfo struct {
	ID      ids.ShardID
	Size    int64
	Created time.Time
}

// Info reports drive identity, capacity and health.
type Info struct {
	ID       ids.DriveID
	Node     ids.NodeID
	Endpoint string
	Total    uint64
	Free     uint64
	Used     uint64
	Online   bool
}

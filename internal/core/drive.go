package core

import (
	"context"
	"io"
	"time"
)

// ShardInfo describes a stored shard.
type ShardInfo struct {
	ID      ShardID
	Size    int64
	Created time.Time
}

// DriveInfo reports drive identity, capacity and health.
type DriveInfo struct {
	ID       DriveID
	Node     NodeID
	Endpoint string
	Total    uint64
	Free     uint64
	Used     uint64
	Online   bool
}

// Drive stores immutable shards addressed by ShardID. Implemented by
// internal/storage for local drives and by internal/cluster for remote
// drives reached over UniWire. A Drive is safe for concurrent use.
//
// Shard bytes are opaque to the drive; integrity framing (bitrot hashes) is
// added by the erasure layer. Drives additionally protect their own
// on-disk structures (record headers) with checksums.
type Drive interface {
	// ID returns the drive id from its format record.
	ID() DriveID

	// WriteShard stores exactly size bytes read from r as shard id. It
	// returns nil only after the data is durable according to the drive's
	// sync policy (fsync by default). If r yields fewer or more than size
	// bytes, or returns an error, nothing is stored and an error is returned.
	// Writing an existing id fails with ErrShardExists.
	WriteShard(ctx context.Context, id ShardID, r io.Reader, size int64) error

	// ReadShard returns a reader for length bytes starting at offset. A
	// negative length reads to the end. It fails with ErrShardNotFound for
	// unknown ids and with an error wrapping ErrCorrupt if the drive's own
	// structural checks fail. Callers must Close the reader.
	ReadShard(ctx context.Context, id ShardID, offset, length int64) (io.ReadCloser, error)

	// StatShard returns shard metadata or ErrShardNotFound.
	StatShard(ctx context.Context, id ShardID) (ShardInfo, error)

	// DeleteShard removes a shard. Deleting a missing shard returns
	// ErrShardNotFound. Space may be reclaimed asynchronously.
	DeleteShard(ctx context.Context, id ShardID) error

	// WalkShards calls fn for every stored shard, in no particular order,
	// until fn returns an error or ctx is done. Used by GC and repair.
	WalkShards(ctx context.Context, fn func(ShardInfo) error) error

	// Info reports identity, capacity and health.
	Info(ctx context.Context) (DriveInfo, error)

	// Close releases resources. Calls after Close fail with ErrClosed.
	Close() error
}

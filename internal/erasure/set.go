package erasure

import (
	"context"
	"io"

	"github.com/eukalpia/unio/internal/ids"
)

// Set stores and retrieves data on a fixed group of drives with erasure
// coding and bitrot protection. Safe for concurrent use.
type Set interface {
	// ID returns the set id.
	ID() ids.SetID

	// Shape returns the data and parity shard counts.
	Shape() (dataShards, parityShards int)

	// Write encodes exactly size bytes from r and writes the shards of id to
	// the set's drives in parallel. It returns the layout of the shards that
	// became durable. Fewer than the write quorum fails with an error
	// wrapping ErrWriteQuorum after removing the shards that were written.
	// If r returns an error, Write returns it (wrapped) and removes written
	// shards; the caller must not commit metadata.
	Write(ctx context.Context, id ids.DataID, r io.Reader, size int64) (Layout, error)

	// Read returns a reader for [offset, offset+length) of the size-byte data
	// stored under id with layout. It reads data shards directly when they
	// are healthy and reconstructs from parity otherwise. Every block is
	// verified; damaged shards are reported for repair. If the data cannot
	// be served correctly, Read (or a later Read on the returned reader)
	// fails with an error wrapping ErrReadQuorum or ErrCorrupt.
	Read(ctx context.Context, id ids.DataID, layout Layout, size, offset, length int64) (io.ReadCloser, error)

	// Delete removes all shards of id. Missing shards are not an error.
	// Unreachable drives are reported in the returned error; their shards
	// are reclaimed later by garbage collection.
	Delete(ctx context.Context, id ids.DataID, layout Layout) error

	// Heal rewrites missing or corrupt shards of id and returns the updated
	// layout. It returns the input layout unchanged if nothing was repaired.
	Heal(ctx context.Context, id ids.DataID, layout Layout, size int64) (Layout, error)
}

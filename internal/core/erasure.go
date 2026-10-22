package core

import (
	"context"
	"io"
)

// ErasureLayout records where the shards of one DataID were written. It is
// stored in object metadata, which makes placement explicit rather than
// recomputed (see architecture section 3).
type ErasureLayout struct {
	Set          SetID
	DataShards   int
	ParityShards int
	// StripeSize is the number of data bytes per erasure stripe.
	StripeSize int
	// Drives has DataShards+ParityShards entries; entry i is the drive that
	// holds shard i. A zero DriveID marks a shard that was not written.
	Drives []DriveID
}

// TotalShards returns DataShards + ParityShards.
func (l ErasureLayout) TotalShards() int { return l.DataShards + l.ParityShards }

// WriteQuorum is the minimum number of shards that must be durable for a
// write to succeed: K, or K+1 when K equals M.
func (l ErasureLayout) WriteQuorum() int {
	if l.DataShards == l.ParityShards {
		return l.DataShards + 1
	}
	return l.DataShards
}

// ErasureSet stores and retrieves data on a fixed group of drives with
// Reed-Solomon coding and bitrot protection. Implemented by
// internal/erasure. Safe for concurrent use.
type ErasureSet interface {
	// ID returns the set id.
	ID() SetID

	// Shape returns the data and parity shard counts.
	Shape() (dataShards, parityShards int)

	// Write encodes exactly size bytes from r and writes the shards of id to
	// the set's drives in parallel. It returns the layout of the shards that
	// became durable. Fewer than the write quorum fails with an error
	// wrapping ErrWriteQuorum after removing the shards that were written.
	// If r returns an error, Write returns it (wrapped) and removes written
	// shards; the caller must not commit metadata.
	Write(ctx context.Context, id DataID, r io.Reader, size int64) (ErasureLayout, error)

	// Read returns a reader for [offset, offset+length) of the size-byte
	// data stored under id with layout. It reads data shards directly when
	// they are healthy and reconstructs from parity otherwise. Every block is
	// verified; damaged shards are reported to the heal queue. If the data
	// cannot be served correctly, Read (or a later Read on the returned
	// reader) fails with an error wrapping ErrReadQuorum or ErrCorrupt.
	Read(ctx context.Context, id DataID, layout ErasureLayout, size, offset, length int64) (io.ReadCloser, error)

	// Delete removes all shards of id. Missing shards are not an error.
	// Unreachable drives are reported in the returned error; their shards
	// are reclaimed later by garbage collection.
	Delete(ctx context.Context, id DataID, layout ErasureLayout) error

	// Heal rewrites missing or corrupt shards of id and returns the updated
	// layout. It returns the input layout unchanged if nothing was repaired.
	Heal(ctx context.Context, id DataID, layout ErasureLayout, size int64) (ErasureLayout, error)
}

// SetRegistry resolves erasure sets for placement and reads. Implemented by
// the node assembly from the cluster topology.
type SetRegistry interface {
	// Place picks the set for a new object, skipping sets that are too full.
	Place(ctx context.Context, bucket, key string, size int64) (ErasureSet, error)
	// Get returns the set with the given id.
	Get(id SetID) (ErasureSet, error)
}

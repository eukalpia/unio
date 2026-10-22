package local

import (
	"errors"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble/v2"

	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// SyncPolicy selects when an acknowledged write reaches stable storage.
type SyncPolicy int

// Sync policies.
const (
	// SyncAlways makes WriteShard and DeleteShard return only after the data
	// and the index are durable. This is the default.
	SyncAlways SyncPolicy = iota
	// SyncBatch acknowledges writes once they reach the page cache and syncs
	// at most every BatchInterval. A power loss can lose acknowledged writes
	// from the last interval. Lost records are checksummed, so they read as
	// ErrCorrupt and never as wrong bytes.
	SyncBatch
)

// Defaults for Options.
const (
	DefaultPackedMaxShardSize int64 = 1 << 20
	DefaultSegmentSize        int64 = 256 << 20
	DefaultBufferBudget       int64 = 256 << 20
	DefaultCompactRate        int64 = 64 << 20
	DefaultBatchInterval            = 50 * time.Millisecond

	// maxPackedMaxShardSize bounds the size of a packed shard.
	maxPackedMaxShardSize int64 = 16 << 20
	// smallRecordLimit is the data size up to which packed I/O uses the small
	// buffer class.
	smallRecordLimit int64 = 64 << 10
	// blobChunk is the write buffer size for blobs. It is a multiple of the
	// 4 KiB O_DIRECT alignment.
	blobChunk int64 = 1 << 20
)

// Options configures a drive. The zero value selects every default.
type Options struct {
	// ClusterID, when set, must match the cluster id in the drive's format
	// record. Open refuses drives that belong to another cluster.
	ClusterID string
	// ExpectedID, when non-zero, must match the drive id in the format record.
	// Open refuses a drive whose id differs, with ErrDriveIDMismatch, so that a
	// drive moved to another slot cannot serve that slot's shards.
	ExpectedID ids.DriveID
	// NodeID and Endpoint are reported by Info.
	NodeID   ids.NodeID
	Endpoint string

	// PackedMaxShardSize is the largest shard stored in a segment. Larger
	// shards become blob files. Zero selects DefaultPackedMaxShardSize.
	PackedMaxShardSize int64
	// SegmentSize is the capacity of a segment file. Zero selects
	// DefaultSegmentSize.
	SegmentSize int64
	// SyncPolicy selects the durability mode. The default is SyncAlways.
	SyncPolicy SyncPolicy
	// BatchInterval is the sync period under SyncBatch. Zero selects
	// DefaultBatchInterval.
	BatchInterval time.Duration
	// IndexCache is a block cache shared with other drives. Nil selects
	// Pebble's default cache.
	IndexCache *pebble.Cache
	// BufferBudget bounds the memory used for I/O buffers. Zero selects
	// DefaultBufferBudget.
	BufferBudget int64
	// CompactRate bounds the bytes per second read by compaction. Zero
	// selects DefaultCompactRate, and a negative value disables compaction.
	CompactRate int64

	// hooks injects faults in tests of this package.
	hooks *faults.Hooks
}

// resolved is Options with defaults applied and values checked.
type resolved struct {
	Options
	packedMax   int64
	segmentSize int64
	budget      int64
	compactRate int64
	batchEvery  time.Duration
}

// resolve applies defaults and validates the options.
func (o Options) resolve() (resolved, error) {
	r := resolved{Options: o}
	r.packedMax = o.PackedMaxShardSize
	if r.packedMax == 0 {
		r.packedMax = DefaultPackedMaxShardSize
	}
	r.segmentSize = o.SegmentSize
	if r.segmentSize == 0 {
		r.segmentSize = DefaultSegmentSize
	}
	r.budget = o.BufferBudget
	if r.budget == 0 {
		r.budget = DefaultBufferBudget
	}
	r.compactRate = o.CompactRate
	if r.compactRate == 0 {
		r.compactRate = DefaultCompactRate
	}
	r.batchEvery = o.BatchInterval
	if r.batchEvery <= 0 {
		r.batchEvery = DefaultBatchInterval
	}
	switch {
	case r.packedMax <= 0 || r.packedMax > maxPackedMaxShardSize:
		return r, fmt.Errorf("local: packed shard size %d out of range", r.packedMax)
	case r.segmentSize < smallRecordLimit || r.segmentSize > 1<<40:
		return r, fmt.Errorf("local: segment size %d out of range", r.segmentSize)
	case r.packedMax+segment.HeaderSize > r.segmentSize:
		return r, errors.New("local: a segment must hold at least one packed shard")
	case o.SyncPolicy != SyncAlways && o.SyncPolicy != SyncBatch:
		return r, fmt.Errorf("local: unknown sync policy %d", o.SyncPolicy)
	case r.budget < r.bigPoolSize() || r.budget < blobChunk:
		return r, fmt.Errorf("local: buffer budget %d is too small", r.budget)
	}
	return r, nil
}

// bigPoolSize is the buffer size for the largest packed record.
func (r resolved) bigPoolSize() int64 {
	return r.packedMax + segment.HeaderSize
}

// smallPoolSize is the buffer size for packed records up to smallRecordLimit.
func (r resolved) smallPoolSize() int64 {
	return min(r.packedMax, smallRecordLimit) + segment.HeaderSize
}

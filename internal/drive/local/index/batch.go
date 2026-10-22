package index

import (
	"fmt"

	"github.com/cockroachdb/pebble/v2"

	"github.com/eukalpia/unio/internal/drive/local/internal/syncerr"
	"github.com/eukalpia/unio/internal/ids"
)

// Batch collects index updates to be committed atomically. Operations apply
// in the order they were added. A Batch is not safe for concurrent use.
type Batch struct {
	x *Index
	b *pebble.Batch // created by the first operation
}

func (b *Batch) batch() *pebble.Batch {
	if b.b == nil {
		b.b = b.x.db.NewBatch()
	}
	return b.b
}

// PutShard records the location of id.
func (b *Batch) PutShard(id ids.ShardID, loc Location) error {
	if err := b.batch().Set(shardKey(id), encodeLocation(loc), pebble.NoSync); err != nil {
		return fmt.Errorf("index: stage put %s: %w", id, err)
	}
	return nil
}

// DeleteShard removes id from the index.
func (b *Batch) DeleteShard(id ids.ShardID) error {
	if err := b.batch().Delete(shardKey(id), pebble.NoSync); err != nil {
		return fmt.Errorf("index: stage delete %s: %w", id, err)
	}
	return nil
}

// PutSegment records the metadata of segment seg.
func (b *Batch) PutSegment(seg uint64, m SegmentMeta) error {
	if err := b.batch().Set(segmentKey(seg), encodeSegmentMeta(m), pebble.NoSync); err != nil {
		return fmt.Errorf("index: stage segment %d: %w", seg, err)
	}
	return nil
}

// DeleteSegment removes the metadata of segment seg.
func (b *Batch) DeleteSegment(seg uint64) error {
	if err := b.batch().Delete(segmentKey(seg), pebble.NoSync); err != nil {
		return fmt.Errorf("index: stage delete segment %d: %w", seg, err)
	}
	return nil
}

// Len returns the number of staged operations.
func (b *Batch) Len() int {
	if b.b == nil {
		return 0
	}
	return int(b.b.Count())
}

// Commit applies the batch atomically. With sync set, the call returns after
// the write-ahead log is on stable storage. The batch is consumed either way.
func (b *Batch) Commit(sync bool) error {
	return b.x.read(func(db *pebble.DB) error {
		if b.b == nil {
			return nil
		}
		n := b.Len()
		wo := pebble.NoSync
		if sync {
			wo = pebble.Sync
		}
		err := db.Apply(b.b, wo)
		_ = b.b.Close()
		b.b = nil
		if err != nil {
			if sync {
				// A failed write of the write-ahead log is treated as a failed sync:
				// the drive cannot tell which of the two failed.
				return syncerr.Wrap("index wal sync", b.x.dir, err)
			}
			return fmt.Errorf("index: commit %d operations: %w", n, err)
		}
		return nil
	})
}

// Discard drops the batch without applying it.
func (b *Batch) Discard() error {
	if b.b == nil {
		return nil
	}
	err := b.b.Close()
	b.b = nil
	if err != nil {
		return fmt.Errorf("index: discard batch: %w", err)
	}
	return nil
}

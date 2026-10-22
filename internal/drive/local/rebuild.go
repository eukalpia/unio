package local

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// rebuildBatch is the number of index operations committed at a time while
// rebuilding. Each batch is durable on its own, and the final batch is synced.
const rebuildBatch = 4096

// rebuildIndex fills the empty index from the drive's files. Segments are
// replayed in id order, so the last record for each shard wins, which is the
// same rule that compaction relies on. Each segment is verified in full, and
// the active segment is trimmed to its last valid record. Blob files are then
// indexed by their size and modification time.
// It returns, per segment, the byte ranges that were skipped because no valid
// record starts in them.
func (d *Drive) rebuildIndex(ctx context.Context) (map[uint64][]segment.Range, error) {
	files, err := d.lay.listSegments()
	if err != nil {
		return nil, err
	}
	rb := &rebuilder{d: d, ctx: ctx, batch: d.idx.Batch(), damaged: map[uint64][]segment.Range{}}
	for i, id := range files {
		last := i == len(files)-1
		if err := rb.segment(id, last); err != nil {
			return nil, err
		}
	}
	if err := rb.blobs(); err != nil {
		return nil, err
	}
	if err := rb.commit(true); err != nil {
		return nil, err
	}
	return rb.damaged, nil
}

// rebuilder accumulates index operations and commits them in bounded batches.
type rebuilder struct {
	d       *Drive
	ctx     context.Context
	batch   *index.Batch
	pending int
	damaged map[uint64][]segment.Range
}

// commit applies the pending batch and starts an empty one.
func (rb *rebuilder) commit(sync bool) error {
	if err := rb.batch.Commit(sync); err != nil {
		return err
	}
	rb.batch = rb.d.idx.Batch()
	rb.pending = 0
	return nil
}

// op counts one index operation and commits when the batch is full.
func (rb *rebuilder) op() error {
	rb.pending++
	if rb.pending < rebuildBatch {
		return nil
	}
	return rb.commit(false)
}

// segment replays one segment file and records its metadata. Only the last
// segment is active, so only it may be trimmed.
func (rb *rebuilder) segment(id uint64, last bool) error {
	path := rb.d.lay.segmentPath(id)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	created := info.ModTime().UnixNano()
	f, err := segment.Open(path, rb.d.opts.segmentSize, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	size, err := f.Size()
	if err != nil {
		return err
	}
	res, err := segment.Resync(f, size, func(rec segment.Record) error {
		if err := rb.ctx.Err(); err != nil {
			return err
		}
		if rec.Header.Flags&segment.FlagTombstone != 0 {
			if err := rb.batch.DeleteShard(rec.Header.ShardID); err != nil {
				return err
			}
		} else {
			loc := index.Location{
				Kind:    index.KindPacked,
				Segment: id,
				Offset:  rec.Offset,
				Length:  int64(rec.Header.Length),
				Created: created,
			}
			if err := rb.batch.PutShard(rec.Header.ShardID, loc); err != nil {
				return err
			}
		}
		return rb.op()
	})
	if err != nil {
		return fmt.Errorf("scan segment %d: %w", id, err)
	}
	end := res.End
	if len(res.Damaged) > 0 {
		rb.damaged[id] = res.Damaged
		logDamage(rb.d.lay.root, id, res.Damaged)
	}
	if last && end < size {
		if err := f.Truncate(end); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return err
		}
	}
	if err := rb.batch.PutSegment(id, index.SegmentMeta{End: end, Sealed: !last}); err != nil {
		return err
	}
	return rb.op()
}

// blobs indexes every blob file. Blobs are visited after segments, so a blob
// placed after a packed record of the same id takes precedence.
func (rb *rebuilder) blobs() error {
	return rb.d.blobs.Walk(func(id ids.ShardID, _ string, size int64, modTime time.Time) error {
		if err := rb.ctx.Err(); err != nil {
			return err
		}
		loc := index.Location{Kind: index.KindBlob, Length: size, Created: modTime.UnixNano()}
		if err := rb.batch.PutShard(id, loc); err != nil {
			return err
		}
		return rb.op()
	})
}

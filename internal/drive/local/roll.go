package local

import (
	"fmt"
	"os"

	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
	"github.com/eukalpia/unio/internal/drive/local/internal/syncerr"
	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// roll seals the active segment at its committed end and starts a new one.
// The seal and the new segment's metadata commit together, so the index always
// names exactly one active segment. Before sealing, the old file is truncated
// to its end, so sealed segments hold no preallocated or uncommitted bytes.
func (d *Drive) roll() error {
	old := d.active
	if err := old.Truncate(old.End()); err != nil {
		return err
	}
	if err := old.Sync(); err != nil {
		return d.markFaulty(err)
	}
	newID := d.nextID
	next, err := d.createSegment(newID)
	if err != nil {
		return err
	}
	b := d.idx.Batch()
	if err := b.PutSegment(d.activeID, index.SegmentMeta{End: old.End(), Sealed: true}); err != nil {
		return d.abandon(next, err)
	}
	if err := b.PutSegment(newID, index.SegmentMeta{}); err != nil {
		return d.abandon(next, err)
	}
	if err := b.Commit(true); err != nil {
		return d.abandon(next, d.markFaulty(err))
	}

	d.segs.add(newID, next)
	d.stats.seal(d.activeID, old.End())
	d.stats.set(newID, segStat{})
	d.active = next
	d.activeID = newID
	d.nextID++
	d.committedEnd = 0
	d.poisoned = false
	d.signal()
	return nil
}

// abandon removes a segment that was created but never committed and returns
// err, so that the next open does not mistake it for part of the drive.
func (d *Drive) abandon(f *segment.File, err error) error {
	_ = f.Close()
	_ = os.Remove(f.Path())
	return err
}

// createSegment creates the file of segment id and makes its directory entry
// durable. The index is not updated.
func (d *Drive) createSegment(id uint64) (*segment.File, error) {
	path := d.lay.segmentPath(id)
	f, err := segment.Create(path, d.opts.segmentSize)
	if err != nil {
		return nil, fmt.Errorf("local: create segment %d: %w", id, err)
	}
	err = d.opts.hooks.Check(faults.DirSync)
	if err == nil {
		err = syncDir(d.lay.segments)
	}
	if err != nil {
		return nil, d.abandon(f, d.markFaulty(syncerr.Wrap("fsync dir", d.lay.segments, err)))
	}
	return f, nil
}

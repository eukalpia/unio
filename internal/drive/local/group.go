package local

import (
	"errors"
	"fmt"
	"os"

	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
	"github.com/eukalpia/unio/internal/drive/local/internal/syncerr"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// maxGroup bounds how many requests one commit covers.
const maxGroup = 512

// overlay is the effect a staged request has on one shard. It lets later
// requests in the same group see earlier ones before they are committed.
type overlay struct {
	loc     index.Location
	present bool
}

// group is a set of requests committed together. Their segment appends share
// one fdatasync and their index updates share one Pebble commit: this is group
// commit. Requests are answered only after the commit.
type group struct {
	d *Drive

	reqs     []*request // staged, answered at flush
	batch    *index.Batch
	ov       map[ids.ShardID]overlay
	deltas   map[uint64]int64 // live-byte change per segment
	appended bool             // the active segment has uncommitted bytes
	placed   []string         // blob files renamed into place by this group
	retired  []uint64         // segments whose retirement is staged
}

func (d *Drive) newGroup() *group {
	g := &group{d: d}
	g.reset()
	return g
}

// reset clears the group for the next batch of requests.
func (g *group) reset() {
	g.reqs = g.reqs[:0]
	g.batch = g.d.idx.Batch()
	g.ov = make(map[ids.ShardID]overlay)
	g.deltas = make(map[uint64]int64)
	g.appended = false
	g.placed = g.placed[:0]
	g.retired = g.retired[:0]
}

// lookup returns the location of id as it will be once the group commits.
func (g *group) lookup(id ids.ShardID) (index.Location, bool, error) {
	if o, ok := g.ov[id]; ok {
		return o.loc, o.present, nil
	}
	loc, err := g.d.idx.Get(id)
	if errors.Is(err, index.ErrNotFound) {
		return index.Location{}, false, nil
	}
	if err != nil {
		return index.Location{}, false, err
	}
	return loc, true, nil
}

// stagePut records that id now lives at loc.
func (g *group) stagePut(id ids.ShardID, loc index.Location) error {
	if err := g.batch.PutShard(id, loc); err != nil {
		return err
	}
	g.ov[id] = overlay{loc: loc, present: true}
	return nil
}

// stageUnset records that id is gone.
func (g *group) stageUnset(id ids.ShardID) error {
	if err := g.batch.DeleteShard(id); err != nil {
		return err
	}
	g.ov[id] = overlay{}
	return nil
}

// flush commits the staged requests and answers them. It returns the commit
// error, which every staged request also received.
func (g *group) flush() error {
	if len(g.reqs) == 0 {
		return nil
	}
	err := g.d.commit(g)
	g.complete(err)
	return err
}

// complete publishes a commit's outcome. On success it updates the accounting
// and answers the requests. On failure it undoes what the group did outside
// the index: placed blobs are removed, and the uncommitted tail of the active
// segment is dropped. The next roll then seals the segment at its committed
// end.
func (g *group) complete(err error) {
	d := g.d
	if err != nil {
		for _, p := range g.placed {
			_ = os.Remove(p)
		}
		if g.appended {
			d.active.SetEnd(d.committedEnd)
			d.poisoned = true
		}
		for _, r := range g.reqs {
			r.reply <- err
		}
		g.reset()
		return
	}
	d.committedEnd = d.active.End()
	d.stats.apply(g.deltas, d.activeID, d.active.End())
	for _, r := range g.reqs {
		r.reply <- d.afterCommit(r)
	}
	for _, seg := range g.retired {
		d.stats.remove(seg)
	}
	g.reset()
}

// afterCommit finishes a request whose index change is durable. Blob deletes
// unlink their file here, after the index no longer points at it. Retired
// segments leave the table here, once they are unreferenced by the index.
func (d *Drive) afterCommit(r *request) error {
	switch r.kind {
	case reqDelete:
		if r.from.Kind != index.KindBlob {
			return nil
		}
		if err := d.blobs.Remove(r.final); err != nil && !errors.Is(err, os.ErrNotExist) {
			return d.checkSync(err)
		}
	case reqRetire:
		return d.segs.retire(r.seg)
	}
	return nil
}

// commit makes the staged work durable: segment bytes first, then the index.
// The index commit includes the committed end of the active segment, so a
// crash between the two leaves only uncommitted bytes past the end.
//
// A failed fsync makes the drive faulty and every request of the group fails
// with it, because each of them depends on that fsync. A failure before
// anything reaches the disk is ordinary and recoverable.
func (d *Drive) commit(g *group) error {
	if g.appended {
		if d.opts.SyncPolicy == SyncAlways {
			if err := d.opts.hooks.Check(faults.SegmentSync); err != nil {
				return d.markFaulty(syncerr.Wrap("fdatasync", d.active.Path(), err))
			}
			if err := d.active.Sync(); err != nil {
				return d.markFaulty(err)
			}
		} else {
			d.dirty = true
		}
		if err := g.batch.PutSegment(d.activeID, index.SegmentMeta{End: d.active.End()}); err != nil {
			return err
		}
	}
	if err := d.opts.hooks.Check(faults.IndexCommit); err != nil {
		return err
	}
	sync := d.opts.SyncPolicy == SyncAlways
	if !sync {
		d.dirty = true
		return g.batch.Commit(false)
	}
	if err := d.opts.hooks.Check(faults.IndexSync); err != nil {
		return d.markFaulty(syncerr.Wrap("index wal sync", d.lay.index, err))
	}
	if err := g.batch.Commit(true); err != nil {
		return d.markFaulty(err)
	}
	return nil
}

// appendRecord appends rec to the active segment, rolling to a new segment
// first if needed. It must be called with a group that has no uncommitted
// bytes beyond the committed end, or after flushing.
func (d *Drive) appendRecord(g *group, rec []byte) (int64, error) {
	if err := g.ensureRoom(int64(len(rec))); err != nil {
		return 0, err
	}
	if err := d.opts.hooks.Check(faults.SegmentAppend); err != nil {
		return 0, err
	}
	off, err := d.active.Append(rec)
	if err != nil {
		return 0, fmt.Errorf("local: append record: %w", err)
	}
	g.appended = true
	return off, nil
}

// ensureRoom makes space for n bytes in the active segment. When the segment
// is full or poisoned by a failed commit, the group is flushed first and the
// segment is rolled.
func (g *group) ensureRoom(n int64) error {
	d := g.d
	if !d.poisoned && d.active.End()+n <= d.active.Capacity() {
		return nil
	}
	if err := g.flush(); err != nil {
		return err
	}
	if n > d.active.Capacity() {
		return segment.ErrFull
	}
	return d.roll()
}

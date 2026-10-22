package local

import (
	"fmt"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/compact"
	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// The stage functions run in the committer. Each one checks its condition
// against the group's view of the index, makes any side effect that cannot be
// undone (appends, renames), and stages the index change. A failure before
// staging leaves nothing behind.

// stagePacked stores a new packed record unless the shard exists.
func (d *Drive) stagePacked(g *group, r *request) error {
	if _, present, err := g.lookup(r.id); err != nil {
		return err
	} else if present {
		return drive.ErrShardExists
	}
	off, err := d.appendRecord(g, r.rec)
	if err != nil {
		return err
	}
	loc := index.Location{
		Kind:    index.KindPacked,
		Segment: d.activeID,
		Offset:  off,
		Length:  int64(len(r.rec)) - segment.HeaderSize,
		Created: r.created,
	}
	if err := g.stagePut(r.id, loc); err != nil {
		return err
	}
	g.deltas[d.activeID] += int64(len(r.rec))
	return nil
}

// stageBlob places a written blob unless the shard exists.
func (d *Drive) stageBlob(g *group, r *request) error {
	if _, present, err := g.lookup(r.id); err != nil {
		return err
	} else if present {
		return drive.ErrShardExists
	}
	if err := d.opts.hooks.Check(faults.BlobPlace); err != nil {
		return err
	}
	if err := d.blobs.Place(r.tmp, r.final); err != nil {
		return err
	}
	g.placed = append(g.placed, r.final)
	return g.stagePut(r.id, index.Location{Kind: index.KindBlob, Length: r.size, Created: r.created})
}

// stageDelete deletes a shard. A packed shard gets a durable tombstone, which
// records the segment of the deleted record so that rebuilds and compaction can
// tell whether the tombstone is still needed. A blob is unlinked after the
// index commit.
func (d *Drive) stageDelete(g *group, r *request) error {
	loc, present, err := g.lookup(r.id)
	if err != nil {
		return err
	}
	if !present {
		return drive.ErrShardNotFound
	}
	r.from = loc
	if loc.Kind == index.KindBlob {
		r.final = d.blobs.Path(r.id)
		return g.stageUnset(r.id)
	}
	rec := segment.AppendRecord(nil, segment.FlagTombstone, r.id, segment.TombstoneOrigin(loc.Segment))
	if _, err := d.appendRecord(g, rec); err != nil {
		return err
	}
	if err := g.stageUnset(r.id); err != nil {
		return err
	}
	g.deltas[loc.Segment] -= segment.RecordSize(loc.Length)
	return nil
}

// stageCopy moves a live record to the active segment. It runs only if the
// shard still lives where compaction read it, so a concurrent overwrite or
// delete cannot be undone by a stale copy.
func (d *Drive) stageCopy(g *group, r *request) error {
	cur, present, err := g.lookup(r.id)
	if err != nil {
		return err
	}
	if !present || cur != r.from {
		return errSkipped
	}
	off, err := d.appendRecord(g, r.rec)
	if err != nil {
		return err
	}
	loc := index.Location{
		Kind:    index.KindPacked,
		Segment: d.activeID,
		Offset:  off,
		Length:  cur.Length,
		Created: cur.Created,
	}
	if err := g.stagePut(r.id, loc); err != nil {
		return err
	}
	size := segment.RecordSize(cur.Length)
	g.deltas[r.from.Segment] -= size
	g.deltas[d.activeID] += size
	return nil
}

// stageCopyTomb carries a tombstone forward while it may still be needed. A
// tombstone is needed while its origin segment exists and the shard has no
// newer record: then, if the index is lost, a rebuild would otherwise bring
// back the deleted record. Copies are appended at the end, so they never
// reorder a tombstone past a newer live record.
func (d *Drive) stageCopyTomb(g *group, r *request) error {
	if _, ok := d.stats.segEnd(r.origin); !ok || r.origin >= r.seg {
		return errSkipped
	}
	if _, present, err := g.lookup(r.id); err != nil {
		return err
	} else if present {
		return errSkipped
	}
	_, err := d.appendRecord(g, r.rec)
	return err
}

// stageRetire removes a segment that no index entry references. It is refused
// for the active segment, and while live bytes remain counting this group.
func (d *Drive) stageRetire(g *group, r *request) error {
	if r.seg == d.activeID {
		return fmt.Errorf("local: cannot retire active segment %d", r.seg)
	}
	live, ok := d.stats.liveOf(r.seg)
	if !ok {
		return compact.ErrGone
	}
	if live+g.deltas[r.seg] != 0 {
		return compact.ErrLive
	}
	g.retired = append(g.retired, r.seg)
	return g.batch.DeleteSegment(r.seg)
}

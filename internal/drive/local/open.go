package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/blob"
	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// Open opens the drive formatted at path. It checks the format record against
// opts.ClusterID, then loads the index. If the index is missing or disagrees
// with the segment files, the index is rebuilt from them. Bytes past each
// segment's committed end are discarded, and blob files that the index does
// not reference are removed.
func Open(ctx context.Context, path string, opts Options) (*Drive, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := opts.resolve()
	if err != nil {
		return nil, err
	}
	lay := newLayout(path)
	id, err := readFormat(lay, opts.ClusterID)
	if err != nil {
		return nil, err
	}
	for _, dir := range lay.dirs() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("local: create %s: %w", dir, err)
		}
	}

	d := &Drive{
		id:        id,
		opts:      r,
		lay:       lay,
		stats:     newSegStats(),
		segs:      newSegTable(),
		pools:     newPools(r),
		reqs:      make(chan *request),
		stop:      make(chan struct{}),
		committed: make(chan struct{}),
		wake:      make(chan struct{}, 1),
	}
	d.blobs, err = blob.NewStore(lay.blobs, lay.tmp, d.pools.blob)
	if err != nil {
		return nil, err
	}
	if err := d.load(ctx); err != nil {
		d.abortOpen()
		return nil, err
	}
	go d.runCommitter()
	d.startCompaction()
	return d, nil
}

// load prepares the index, segments and blob directory. It runs before the
// committer starts, so it may touch the drive's state directly.
func (d *Drive) load(ctx context.Context) error {
	rebuilt, err := d.openIndex()
	if err != nil {
		return err
	}
	if rebuilt {
		if err := d.rebuildIndex(ctx); err != nil {
			return fmt.Errorf("local: rebuild index: %w", err)
		}
		if err := d.promoteRebuiltIndex(); err != nil {
			return err
		}
	}
	if err := d.recoverSegments(); err != nil {
		return err
	}
	return d.sweepBlobs()
}

// openIndex opens the index. It reports true when the index had to be replaced
// by an empty one, which the caller then fills by scanning the drive. The
// unusable index directory is renamed aside, not deleted, so that nothing is
// destroyed by a misdiagnosis.
func (d *Drive) openIndex() (bool, error) {
	idx, err := index.Open(d.lay.index, d.opts.IndexCache)
	if err == nil {
		ok, cerr := d.indexConsistent(idx)
		if cerr == nil && ok {
			d.idx = idx
			return false, nil
		}
		_ = idx.Close()
	}
	aside := fmt.Sprintf("%s.bad-%d", d.lay.index, time.Now().UnixNano())
	if err := os.Rename(d.lay.index, aside); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("local: set aside index: %w", err)
	}
	// The rebuild goes to a side directory. It is installed only when complete.
	if err := os.RemoveAll(d.lay.indexBuild); err != nil {
		return false, fmt.Errorf("local: clear index rebuild: %w", err)
	}
	idx, err = index.Open(d.lay.indexBuild, d.opts.IndexCache)
	if err != nil {
		return false, err
	}
	d.idx = idx
	return true, nil
}

// promoteRebuiltIndex installs a completed rebuild as the index. A crash before
// the rename leaves the side directory behind, and the next open rebuilds again.
func (d *Drive) promoteRebuiltIndex() error {
	if err := d.idx.Close(); err != nil {
		return fmt.Errorf("local: close rebuilt index: %w", err)
	}
	if err := os.Rename(d.lay.indexBuild, d.lay.index); err != nil {
		return fmt.Errorf("local: install rebuilt index: %w", err)
	}
	idx, err := index.Open(d.lay.index, d.opts.IndexCache)
	if err != nil {
		return err
	}
	d.idx = idx
	return nil
}

// indexConsistent reports whether the index describes exactly the segment files
// on disk: every file has metadata, no metadata lacks a file, every file holds
// at least its committed end, and at most one segment is unsealed.
func (d *Drive) indexConsistent(idx *index.Index) (bool, error) {
	metas, err := idx.Segments()
	if err != nil {
		return false, err
	}
	files, err := d.lay.listSegments()
	if err != nil {
		return false, err
	}
	if len(files) != len(metas) {
		return false, nil
	}
	unsealed := 0
	for _, id := range files {
		m, ok := metas[id]
		if !ok {
			return false, nil
		}
		if !m.Sealed {
			unsealed++
		}
		st, err := os.Stat(d.lay.segmentPath(id))
		if err != nil {
			return false, err
		}
		if st.Size() < m.End {
			return false, nil
		}
	}
	return unsealed <= 1, nil
}

// recoverSegments trims segment files to their committed ends, makes sure there
// is an active segment, opens every segment and computes the accounting.
func (d *Drive) recoverSegments() error {
	metas, err := d.idx.Segments()
	if err != nil {
		return err
	}
	files, err := d.lay.listSegments()
	if err != nil {
		return err
	}
	var maxID uint64
	active := uint64(0)
	for _, id := range files {
		m := metas[id]
		st, err := os.Stat(d.lay.segmentPath(id))
		if err != nil {
			return fmt.Errorf("local: stat segment %d: %w", id, err)
		}
		// Only the active segment can hold uncommitted bytes. A sealed segment
		// is never trimmed, so that no record after a damaged one is destroyed.
		if !m.Sealed && st.Size() > m.End {
			if err := os.Truncate(d.lay.segmentPath(id), m.End); err != nil {
				return fmt.Errorf("local: trim segment %d: %w", id, err)
			}
		}
		if !m.Sealed {
			active = id
		}
		maxID = id
	}
	d.nextID = maxID + 1
	if active == 0 {
		if err := d.startActive(); err != nil {
			return err
		}
		active = d.nextID - 1
	}

	metas, err = d.idx.Segments()
	if err != nil {
		return err
	}
	live, err := d.liveBytes()
	if err != nil {
		return err
	}
	for id, meta := range metas {
		f, err := segment.Open(d.lay.segmentPath(id), d.opts.segmentSize, meta.End)
		if err != nil {
			return fmt.Errorf("local: open segment %d: %w", id, err)
		}
		d.segs.add(id, f)
		d.stats.set(id, segStat{end: meta.End, live: live[id], sealed: id != active})
		if id == active {
			d.active = f
			d.activeID = id
			d.committedEnd = meta.End
		}
	}
	return nil
}

// startActive creates the first segment of a drive, or the successor of a
// sealed last segment, and commits its metadata.
func (d *Drive) startActive() error {
	f, err := d.createSegment(d.nextID)
	if err != nil {
		return err
	}
	b := d.idx.Batch()
	if err := b.PutSegment(d.nextID, index.SegmentMeta{}); err != nil {
		return d.abandon(f, err)
	}
	if err := b.Commit(true); err != nil {
		return d.abandon(f, err)
	}
	_ = f.Close()
	d.nextID++
	return nil
}

// liveBytes sums the record bytes the index references, per segment.
func (d *Drive) liveBytes() (map[uint64]int64, error) {
	live := make(map[uint64]int64)
	err := d.idx.Walk(func(_ ids.ShardID, loc index.Location) error {
		if loc.Kind == index.KindPacked {
			live[loc.Segment] += segment.RecordSize(loc.Length)
		}
		return nil
	})
	return live, err
}

// sweepBlobs removes staging files and blob files the index does not
// reference. Both come from writes that did not commit.
func (d *Drive) sweepBlobs() error {
	if err := d.blobs.CleanTemp(); err != nil {
		return err
	}
	return d.blobs.Walk(func(id ids.ShardID, path string, _ int64, _ time.Time) error {
		_, err := d.lookup(id)
		if errors.Is(err, drive.ErrShardNotFound) {
			if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				return rerr
			}
			return nil
		}
		return err
	})
}

// Package local is the on-disk drive engine (architecture section 8). A drive
// keeps small shards packed into append-only segments with group commit,
// large shards as blob files, and a Pebble index that maps shard ids to their
// locations. Compaction reclaims segments whose records were deleted.
//
// Durability. A write is acknowledged only after its data and index entry are
// durable under the drive's sync policy. A failed fsync makes the drive faulty:
// every write whose durability depended on it fails, later writes return an
// error wrapping drive.ErrFaulty, reads continue, and Info reports the drive
// offline. The fsync is never retried. Under SyncBatch, writes acknowledged
// before the failing fsync stay acknowledged, and the loss window is the batch
// interval.
//
// Rebuild. If the index is lost or disagrees with the segments, Open rebuilds
// it by replaying segments in id order, where the last record for a shard
// wins. A damaged record does not end a segment's replay: the scan resumes at
// the next valid record, and Drive.Recovery lists the skipped byte ranges.
// Checksums detect damage but do not authenticate: a writer who controls a
// payload can embed a complete, valid record, and that record is recovered
// when the bytes around it are damaged.
//
// Known limits.
//
//   - A blob whose unlink fails after its delete is committed stays on disk
//     as an orphan. Open removes orphans when the index is trusted. A rebuild
//     indexes them again, so a later packed record for the same id can be
//     shadowed by the stale blob. Bitrot verification above the drive catches
//     the stale content. The gap is accepted; it can occur on Windows, where a
//     reader can hold the file open.
//   - Tombstones are kept while any older segment exists, so they accumulate
//     for as long as the oldest segment survives.
//   - A sealed segment with damage in its middle is not compacted, because the
//     compaction scan stops at the damage. Its live records are kept, so the
//     space is not reclaimed, but no data is lost.
//   - Only the active segment is trimmed on open. Sealed segments keep bytes
//     past their committed end, which is harmless: their metadata excludes
//     those bytes.
//   - Windows and macOS build but are not exercised by the test suite.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/blob"
	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// Drive is a drive.Drive stored on a local filesystem. It is safe for
// concurrent use.
type Drive struct {
	id    ids.DriveID
	opts  resolved
	lay   layout
	idx   *index.Index
	segs  *segTable
	stats *segStats
	blobs *blob.Store
	pools pools

	reqs      chan *request
	stop      chan struct{}
	committed chan struct{}
	wake      chan struct{}

	compactCancel context.CancelFunc
	compactDone   chan struct{}

	// recovery is set once by Open, before the committer starts.
	recovery Recovery

	// mu is held for reading by every operation and for writing by Close.
	// Close therefore waits for operations in progress and none starts after
	// it has begun. Compaction is stopped before Close takes mu, because
	// compaction holds mu for reading while it works.
	mu     sync.RWMutex
	closed bool

	// The fields below belong to the committer goroutine. They are set up by
	// Open before the committer starts, and read by Close after it exits.
	active       *segment.File
	activeID     uint64
	nextID       uint64
	committedEnd int64
	poisoned     bool
	dirty        bool

	// faultMu guards fault, which the committer, Close and every operation read.
	// Once set, the drive refuses mutations and never retries a sync.
	faultMu sync.Mutex
	fault   error
}

var _ drive.Drive = (*Drive)(nil)

// ID implements drive.Drive.
func (d *Drive) ID() ids.DriveID { return d.id }

// Info implements drive.Drive. Capacity is read from the filesystem that
// holds the drive.
func (d *Drive) Info(ctx context.Context) (drive.Info, error) {
	if err := ctx.Err(); err != nil {
		return drive.Info{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return drive.Info{}, drive.ErrClosed
	}
	total, free, err := capacity(d.lay.root)
	if err != nil {
		return drive.Info{}, fmt.Errorf("local: capacity of %s: %w", d.lay.root, err)
	}
	var used uint64
	if total > free {
		used = total - free
	}
	return drive.Info{
		ID:       d.id,
		Node:     d.opts.NodeID,
		Endpoint: d.opts.Endpoint,
		Total:    total,
		Free:     free,
		Used:     used,
		Online:   d.faultError() == nil,
	}, nil
}

// StatShard implements drive.Drive.
func (d *Drive) StatShard(ctx context.Context, id ids.ShardID) (drive.ShardInfo, error) {
	if err := ctx.Err(); err != nil {
		return drive.ShardInfo{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return drive.ShardInfo{}, drive.ErrClosed
	}
	loc, err := d.lookup(id)
	if err != nil {
		return drive.ShardInfo{}, err
	}
	return drive.ShardInfo{ID: id, Size: loc.Length, Created: time.Unix(0, loc.Created)}, nil
}

// DeleteShard implements drive.Drive.
func (d *Drive) DeleteShard(ctx context.Context, id ids.ShardID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return drive.ErrClosed
	}
	if err := d.submit(ctx, newRequest(reqDelete, id)); err != nil {
		return err
	}
	d.signal()
	return nil
}

// WalkShards implements drive.Drive. It holds no lock while fn runs, so fn may
// call the drive.
func (d *Drive) WalkShards(ctx context.Context, fn func(drive.ShardInfo) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.RLock()
	closed := d.closed
	d.mu.RUnlock()
	if closed {
		return drive.ErrClosed
	}
	err := d.idx.Walk(func(id ids.ShardID, loc index.Location) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn(drive.ShardInfo{ID: id, Size: loc.Length, Created: time.Unix(0, loc.Created)})
	})
	if errors.Is(err, index.ErrClosed) {
		return drive.ErrClosed
	}
	if errors.Is(err, index.ErrCorrupt) {
		return fmt.Errorf("local: walk: %w: %w", drive.ErrCorrupt, err)
	}
	return err
}

// Close implements drive.Drive. It stops compaction, lets the committer finish
// the requests it holds, makes batched writes durable and releases the files.
// Calls after Close fail with drive.ErrClosed.
func (d *Drive) Close() error {
	d.compactCancel()
	<-d.compactDone

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return drive.ErrClosed
	}
	d.closed = true
	close(d.stop)
	<-d.committed

	var errs []error
	_ = d.syncIfDirty() // a failure marks the drive faulty, which is reported below
	if err := d.faultError(); err != nil {
		errs = append(errs, err)
	}
	if err := d.segs.closeAll(); err != nil {
		errs = append(errs, err)
	}
	if err := d.idx.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// lookup returns the index entry of id, mapped to drive errors.
func (d *Drive) lookup(id ids.ShardID) (index.Location, error) {
	loc, err := d.idx.Get(id)
	switch {
	case err == nil:
		return loc, nil
	case errors.Is(err, index.ErrNotFound):
		return index.Location{}, drive.ErrShardNotFound
	case errors.Is(err, index.ErrClosed):
		return index.Location{}, drive.ErrClosed
	case errors.Is(err, index.ErrCorrupt):
		return index.Location{}, corrupt(id, err)
	default:
		return index.Location{}, err
	}
}

// abortOpen releases what Open acquired before it failed.
func (d *Drive) abortOpen() {
	if d.segs != nil {
		_ = d.segs.closeAll()
	}
	if d.idx != nil {
		_ = d.idx.Close()
	}
}

// closeReader adapts a reader and its release function to io.ReadCloser.
type closeReader struct {
	io.Reader
	release func() error
	once    sync.Once
	err     error
}

// Close releases the reader's resources. Only the first call has an effect.
func (c *closeReader) Close() error {
	c.once.Do(func() { c.err = c.release() })
	return c.err
}

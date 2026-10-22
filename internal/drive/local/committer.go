package local

import (
	"fmt"
	"time"

	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
	"github.com/eukalpia/unio/internal/drive/local/internal/syncerr"
)

// runCommitter is the only goroutine that mutates the index or appends to
// segments. Serialising mutations here is what makes the conditional checks
// (exists, location matches) race-free, and it lets concurrent writers share
// one group commit. The loop takes whatever requests are queued, stages them
// into one group and commits it.
func (d *Drive) runCommitter() {
	defer close(d.committed)
	g := d.newGroup()
	var tick <-chan time.Time
	if d.opts.SyncPolicy == SyncBatch {
		t := time.NewTicker(d.opts.batchEvery)
		defer t.Stop()
		tick = t.C
	}
	buf := make([]*request, 0, maxGroup)
	for {
		select {
		case <-d.stop:
			return
		case <-tick:
			_ = d.syncIfDirty() // a failure marks the drive faulty
			continue
		case r := <-d.reqs:
			buf = append(buf[:0], r)
		}
		buf = gather(buf, d.reqs)
		for _, r := range buf {
			d.handle(g, r)
		}
		_ = g.flush() // every request in the group has been answered
	}
}

// gather adds the requests that are already queued to buf, up to maxGroup.
func gather(buf []*request, reqs <-chan *request) []*request {
	for len(buf) < maxGroup {
		select {
		case r := <-reqs:
			buf = append(buf, r)
		default:
			return buf
		}
	}
	return buf
}

// handle stages one request. A request that fails before staging is answered
// at once. A staged request is answered when the group commits.
func (d *Drive) handle(g *group, r *request) {
	if err := d.faultError(); err != nil {
		r.reply <- err // a faulty drive makes no further changes
		return
	}
	var err error
	switch r.kind {
	case reqPut:
		err = d.stagePacked(g, r)
	case reqBlob:
		err = d.stageBlob(g, r)
	case reqDelete:
		err = d.stageDelete(g, r)
	case reqCopy:
		err = d.stageCopy(g, r)
	case reqCopyTomb:
		err = d.stageCopyTomb(g, r)
	case reqRetire:
		err = d.stageRetire(g, r)
	default:
		err = fmt.Errorf("local: unknown request kind %d", r.kind)
	}
	if err != nil {
		r.reply <- err
		return
	}
	g.reqs = append(g.reqs, r)
}

// syncIfDirty makes batched writes durable. It runs on the batch interval
// under SyncBatch, and at Close. A failed sync marks the drive faulty. After a
// fault it does nothing, so the failed fsync is never retried.
func (d *Drive) syncIfDirty() error {
	if !d.dirty || d.faultError() != nil {
		return nil
	}
	if err := d.opts.hooks.Check(faults.SegmentSync); err != nil {
		return d.markFaulty(syncerr.Wrap("fdatasync", d.active.Path(), err))
	}
	if err := d.active.Sync(); err != nil {
		return d.markFaulty(err)
	}
	if err := d.idx.Sync(); err != nil {
		return d.markFaulty(err)
	}
	d.dirty = false
	return nil
}

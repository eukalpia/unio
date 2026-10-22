package local

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/eukalpia/unio/internal/drive/local/compact"
	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// startCompaction launches background compaction unless it is disabled by a
// negative CompactRate. Compaction only plans and reads. Every change to the
// index goes through the committer, which re-checks each move.
func (d *Drive) startCompaction() {
	ctx, cancel := context.WithCancel(context.Background())
	d.compactCancel = cancel
	d.compactDone = make(chan struct{})
	if d.opts.compactRate < 0 {
		cancel()
		close(d.compactDone)
		return
	}
	cfg := compact.Config{
		Limiter: compact.NewLimiter(d.opts.compactRate, d.opts.compactRate),
		Wake:    d.wake,
		OnError: func(err error) {
			slog.Default().Warn("drive compaction pass failed", "drive", d.lay.root, "err", err)
		},
	}
	go func() {
		defer close(d.compactDone)
		compact.Run(ctx, compactTarget{d: d}, cfg)
	}()
}

// compactTarget implements compact.Target over the drive.
type compactTarget struct {
	d *Drive
}

// Sealed implements compact.Target.
func (t compactTarget) Sealed() []compact.Segment {
	return t.d.stats.sealedSegments()
}

// Visit implements compact.Target. It scans the committed part of the segment
// by header. A damaged record ends the scan early, and the segment is then not
// retired, because its live bytes still count as present.
func (t compactTarget) Visit(ctx context.Context, seg uint64, fn func(compact.Record) error) error {
	ref, ok := t.d.segs.acquire(seg)
	if !ok {
		return compact.ErrGone
	}
	defer func() { _ = t.d.segs.release(ref) }()
	end, ok := t.d.stats.segEnd(seg)
	if !ok {
		return compact.ErrGone
	}
	_, err := segment.Scan(ref.file, end, false, func(r segment.Record) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn(compact.Record{Segment: seg, Offset: r.Offset, Header: r.Header})
	})
	return err
}

// Move implements compact.Target. A live packed record is re-read with its
// checksum verified and submitted for copy. A tombstone is submitted for
// carry-forward. The committer decides whether either is still needed.
func (t compactTarget) Move(ctx context.Context, rec compact.Record) (int64, error) {
	d := t.d
	ref, ok := d.segs.acquire(rec.Segment)
	if !ok {
		return 0, compact.ErrGone
	}
	defer func() { _ = d.segs.release(ref) }()

	if rec.Header.Flags&segment.FlagTombstone != 0 {
		return t.moveTombstone(ctx, ref, rec)
	}
	loc, err := d.idx.Get(rec.Header.ShardID)
	if errors.Is(err, index.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if loc.Kind != index.KindPacked || loc.Segment != rec.Segment || loc.Offset != rec.Offset {
		return 0, nil
	}
	buf, err := d.pools.forData(loc.Length).Get(ctx)
	if err != nil {
		return 0, err
	}
	defer buf.Release()
	_, raw, err := ref.file.ReadRecord(rec.Offset, buf.Bytes())
	if err != nil {
		return 0, corrupt(rec.Header.ShardID, err)
	}
	req := newRequest(reqCopy, rec.Header.ShardID)
	req.rec = raw
	req.from = loc
	return moved(d.submit(ctx, req), raw)
}

// moveTombstone reads a tombstone and submits it for carry-forward.
func (t compactTarget) moveTombstone(ctx context.Context, ref *segRef, rec compact.Record) (int64, error) {
	d := t.d
	buf, err := d.pools.small.Get(ctx)
	if err != nil {
		return 0, err
	}
	defer buf.Release()
	_, raw, err := ref.file.ReadRecord(rec.Offset, buf.Bytes())
	if err != nil {
		return 0, corrupt(rec.Header.ShardID, err)
	}
	origin, err := segment.ParseTombstoneOrigin(raw[segment.HeaderSize:])
	if err != nil {
		return 0, corrupt(rec.Header.ShardID, err)
	}
	req := newRequest(reqCopyTomb, rec.Header.ShardID)
	req.rec = raw
	req.origin = origin
	req.seg = rec.Segment
	return moved(d.submit(ctx, req), raw)
}

// moved converts a committer answer for a move into the bytes appended.
func moved(err error, raw []byte) (int64, error) {
	if errors.Is(err, errSkipped) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("local: relocate record: %w", err)
	}
	return int64(len(raw)), nil
}

// Retire implements compact.Target.
func (t compactTarget) Retire(ctx context.Context, seg uint64) error {
	req := newRequest(reqRetire, ids.ShardID{})
	req.seg = seg
	return t.d.submit(ctx, req)
}

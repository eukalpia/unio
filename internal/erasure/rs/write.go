package rs

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/ids"
)

// stripeRead is one stripe read from the input, or the error that stopped
// the read. Exactly one of f and err is set.
type stripeRead struct {
	f   *frame
	err error
}

// Write implements erasure.Set. The input is read one stripe at a time, and
// reading the next stripe overlaps with encoding and writing the current one.
func (s *Set) Write(ctx context.Context, id ids.DataID, r io.Reader, size int64) (erasure.Layout, error) {
	if size < 0 {
		return erasure.Layout{}, fmt.Errorf("erasure/rs: write %s: negative size %d", id, size)
	}
	g, err := s.geomFor(s.opts.StripeSize)
	if err != nil {
		return erasure.Layout{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	n := s.k + s.m
	shardSize := bitrot.ShardSize(size, s.k, g.stripe)
	targets := make([]*target, n)
	for i := range targets {
		targets[i] = newTarget(i, s.drives[i], shardID(id, i), shardSize)
	}
	fo := startFanout(ctx, s.opts.StallTimeout, targets)

	stripes := make(chan stripeRead, 1)
	go s.readInput(ctx, g, r, size, stripes)

	var werr error
	for res := range stripes {
		if res.err != nil {
			werr = res.err
			break
		}
		if werr = s.encode(res.f); werr != nil {
			res.f.release()
			break
		}
		if werr = fo.dispatch(res.f); werr != nil {
			break
		}
	}
	if werr == nil {
		// The reader stops silently when ctx ends; that must not look like a
		// complete input.
		werr = ctx.Err()
	}
	if werr != nil {
		cancel()
	}
	fo.finish()
	// Drain stripes the reader queued before it saw the cancellation, so
	// their buffers return to the pool. The reader closes the channel on exit.
	for res := range stripes {
		if res.f != nil {
			res.f.release()
		}
	}
	return s.commitWrite(ctx, id, g, targets, werr)
}

// commitWrite turns the outcome of a write into a layout, or removes the
// shards that were written and reports the failure.
func (s *Set) commitWrite(ctx context.Context, id ids.DataID, g *geom, targets []*target, werr error) (erasure.Layout, error) {
	layout := erasure.Layout{
		Set:          s.id,
		DataShards:   s.k,
		ParityShards: s.m,
		StripeSize:   g.stripe,
		Drives:       make([]ids.DriveID, len(targets)),
	}
	var written []shardRef
	var failures []error
	for _, t := range targets {
		if t.written() {
			layout.Drives[t.index] = t.drive.ID()
			written = append(written, shardRef{d: t.drive, id: t.shard})
			continue
		}
		failures = append(failures, t.failure())
	}
	if werr == nil && len(written) < layout.WriteQuorum() {
		werr = errors.Join(
			fmt.Errorf("%w: %d of %d shards written, need %d", erasure.ErrWriteQuorum, len(written), len(targets), layout.WriteQuorum()),
			errors.Join(failures...))
	}
	if werr != nil {
		// Best effort: a shard left behind is reclaimed by garbage collection.
		_ = errors.Join(deleteShards(context.WithoutCancel(ctx), written)...)
		return erasure.Layout{}, fmt.Errorf("erasure/rs: write %s: %w", id, werr)
	}
	return layout, nil
}

// encode computes the parity blocks of f in place.
func (s *Set) encode(f *frame) error {
	if s.m == 0 {
		return nil
	}
	if err := s.enc.Encode(f.shards); err != nil {
		return fmt.Errorf("erasure/rs: encode stripe %d: %w", f.stripe, err)
	}
	return nil
}

// readInput cuts the input into frames of data blocks and sends them to out,
// then closes out. It stops silently when ctx ends, and sends the first input
// error otherwise.
func (s *Set) readInput(ctx context.Context, g *geom, r io.Reader, size int64, out chan<- stripeRead) {
	defer close(out)
	stripes := bitrot.Stripes(size, g.stripe)
	for st := range stripes {
		f, err := newFrame(ctx, g, s.k+s.m)
		if err != nil {
			sendStripe(ctx, out, stripeRead{err: err})
			return
		}
		dataLen := int(bitrot.StripeLen(size, g.stripe, st))
		f.setStripe(st, bitrot.BlockLen(size, s.k, g.stripe, st), dataLen)
		if err := fillStripe(r, f, s.k); err != nil {
			f.release()
			sendStripe(ctx, out, stripeRead{err: err})
			return
		}
		if !sendStripe(ctx, out, stripeRead{f: f}) {
			f.release()
			return
		}
	}
	if err := probeEOF(r, size); err != nil {
		sendStripe(ctx, out, stripeRead{err: err})
	}
}

// sendStripe sends res unless ctx ends first, and reports whether it sent.
func sendStripe(ctx context.Context, out chan<- stripeRead, res stripeRead) bool {
	select {
	case out <- res:
		return true
	case <-ctx.Done():
		return false
	}
}

// fillStripe reads the stripe's data into the k data blocks of f and zeroes
// the padding of the last block, so that encoding is deterministic.
func fillStripe(r io.Reader, f *frame, k int) error {
	for i := range k {
		n := min(max(f.dataLen-i*f.bl, 0), f.bl)
		blk := f.shards[i]
		if _, err := io.ReadFull(r, blk[:n]); err != nil {
			return inputError(err)
		}
		clear(blk[n:])
	}
	return nil
}

// probeEOF checks that the input ends exactly at size bytes.
func probeEOF(r io.Reader, size int64) error {
	var b [1]byte
	_, err := io.ReadFull(r, b[:])
	switch {
	case err == nil:
		return fmt.Errorf("erasure/rs: input is longer than the declared size %d", size)
	case errors.Is(err, io.EOF):
		return nil
	default:
		return inputError(err)
	}
}

// inputError wraps an error from the input. Running out of data before the
// declared size is reported as io.ErrUnexpectedEOF.
func inputError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("erasure/rs: input ended before the declared size: %w", io.ErrUnexpectedEOF)
	}
	return fmt.Errorf("erasure/rs: read input: %w", err)
}

package rs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/ids"
)

// Read implements erasure.Set. Data shards are read directly when they are
// healthy. A damaged or unreadable data shard is replaced by parity blocks and
// the stripe is reconstructed. Every damaged shard is queued for repair.
//
// The producer starts at once and takes one frame from the budget per stripe.
// It never waits for budget while holding a frame, and the consumer releases
// each frame as soon as its bytes are copied out. So a read makes progress
// with a budget of a single frame, and N open reads on a budget of N frames
// all drain.
//
// The returned reader fetches stripes in the background, so it must be closed
// even if it is not read to the end.
func (s *Set) Read(ctx context.Context, id ids.DataID, layout erasure.Layout, size, offset, length int64) (io.ReadCloser, error) {
	g, err := s.layoutGeom(layout)
	if err != nil {
		return nil, err
	}
	span, err := bitrot.Span(size, s.k, g.stripe, offset, length)
	if err != nil {
		return nil, fmt.Errorf("erasure/rs: read %s: %w", id, err)
	}
	if length == 0 {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &reader{
		s:        s,
		id:       id,
		layout:   layout,
		g:        g,
		size:     size,
		span:     span,
		pos:      offset,
		end:      offset + length,
		streams:  s.streamsFor(layout, id, size, g.stripe, span.EndStripe),
		reported: make([]bool, s.k+s.m),
		cancel:   cancel,
		out:      make(chan *frame),
	}
	r.wg.Add(1)
	go r.produce(ctx)
	return r, nil
}

// reader streams a byte range of one object. A producer goroutine fetches
// stripes in order, one frame at a time; the consumer copies bytes out of the
// frame it holds. A read holds at most two frames: the one being copied from,
// and the one the producer has fetched and is about to hand over.
type reader struct {
	s        *Set
	id       ids.DataID
	layout   erasure.Layout
	g        *geom
	size     int64
	span     bitrot.Range
	streams  []*stream
	reported []bool

	pos, end int64 // object offsets: next byte to return, and end of the range
	cur      *frame
	out      chan *frame // fetched frames, in stripe order; unbuffered
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	// perr is the producer's terminal error. It is written before out is
	// closed, so the consumer reads it after observing the close.
	perr   error
	err    error
	closed bool
}

// produce fetches the stripes of the range in order and closes out when it
// stops, successfully or not.
func (r *reader) produce(ctx context.Context) {
	defer r.wg.Done()
	defer close(r.out)
	defer r.closeStreams()
	for st := r.span.FirstStripe; st < r.span.EndStripe; st++ {
		// Holding no frame here, the producer may wait for budget.
		f, err := newFrame(ctx, r.g, r.s.k+r.s.m)
		if err != nil {
			r.perr = fmt.Errorf("erasure/rs: read %s: %w", r.id, err)
			return
		}
		if err := r.fetch(ctx, f, st); err != nil {
			f.release()
			r.perr = err
			return
		}
		select {
		case r.out <- f:
		case <-ctx.Done():
			f.release()
			r.perr = fmt.Errorf("erasure/rs: read %s: %w", r.id, context.Cause(ctx))
			return
		}
	}
}

// fetch fills f with stripe st. It reads the data shards, and when some are
// missing it reads parity shards and reconstructs the data.
func (r *reader) fetch(ctx context.Context, f *frame, st int64) error {
	k := r.s.k
	f.setStripe(st, bitrot.BlockLen(r.size, k, r.g.stripe, st), int(bitrot.StripeLen(r.size, r.g.stripe, st)))
	f.reset()
	dataIdx := indexRange(0, k)
	ok, failed, corrupt := gather(ctx, r.streams, dataIdx, st, f, k)
	if err := ctx.Err(); err != nil {
		// A cancelled read fails its shards too; that is not damage.
		return err
	}
	r.report(failed)
	if len(ok) == k {
		return nil
	}
	parityIdx := indexRange(k, k+r.s.m)
	okParity, failedParity, corruptParity := gather(ctx, r.streams, parityIdx, st, f, k-len(ok))
	if err := ctx.Err(); err != nil {
		return err
	}
	r.report(failedParity)
	corrupt = corrupt || corruptParity
	if got := len(ok) + len(okParity); got < k {
		err := fmt.Errorf("erasure/rs: read %s stripe %d: %d of %d shards readable: %w",
			r.id, st, got, k, erasure.ErrReadQuorum)
		if corrupt {
			err = fmt.Errorf("%w (%w)", err, erasure.ErrCorrupt)
		}
		return err
	}
	if err := r.s.enc.ReconstructData(f.shards); err != nil {
		return fmt.Errorf("erasure/rs: reconstruct %s stripe %d: %w", r.id, st, err)
	}
	return nil
}

// report queues each shard in shards for repair, once per read. Requests that
// do not fit in the heal queue are dropped and counted by it.
func (r *reader) report(shards []int) {
	for _, i := range shards {
		if r.reported[i] {
			continue
		}
		r.reported[i] = true
		r.s.heal.Push(HealRequest{Data: r.id, Layout: r.layout, Size: r.size, Shard: i})
	}
}

// closeStreams releases every drive stream the producer opened.
func (r *reader) closeStreams() {
	for _, st := range r.streams {
		st.close()
	}
}

// Read implements io.Reader.
func (r *reader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.pos >= r.end {
		r.err = io.EOF
		return 0, io.EOF
	}
	if r.cur == nil {
		f, ok := <-r.out
		if !ok {
			r.err = r.perr
			if r.err == nil {
				r.err = io.ErrUnexpectedEOF
			}
			return 0, r.err
		}
		r.cur = f
	}
	start := r.cur.stripe * int64(r.g.stripe)
	stop := min(r.end, start+int64(r.cur.dataLen))
	n := r.cur.copyOut(p[:min(int64(len(p)), stop-r.pos)], int(r.pos-start))
	r.pos += int64(n)
	if r.pos == stop {
		// Release before the next receive: the producer may need the budget
		// this frame holds in order to fetch the next stripe.
		r.cur.release()
		r.cur = nil
	}
	return n, nil
}

// Close stops the producer, waits for it, and releases the frame the consumer
// still holds. Frames the producer had not handed over were released by the
// producer itself. It is safe to call more than once.
func (r *reader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.err == nil {
		r.err = errReaderClosed
	}
	r.cancel()
	r.wg.Wait()
	if r.cur != nil {
		r.cur.release()
		r.cur = nil
	}
	return nil
}

// errReaderClosed is returned by Read after Close.
var errReaderClosed = errors.New("erasure/rs: read on closed reader")

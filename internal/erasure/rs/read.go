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

// readFrames is the number of stripes a read holds at once: one being served
// and one being fetched. A read's memory is therefore bounded by two frames,
// whatever the object size.
const readFrames = 2

// Read implements erasure.Set. Data shards are read directly when they are
// healthy. A damaged or unreadable data shard is replaced by parity blocks and
// the stripe is reconstructed. Every damaged shard is queued for repair.
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
		out:      make(chan *frame, readFrames),
		free:     make(chan *frame, readFrames),
	}
	for range readFrames {
		f, err := newFrame(ctx, g, s.k+s.m)
		if err != nil {
			r.drainFrames()
			cancel()
			return nil, fmt.Errorf("erasure/rs: read %s: %w", id, err)
		}
		r.free <- f
	}
	r.wg.Add(1)
	go r.produce(ctx)
	return r, nil
}

// reader streams a byte range of one object. A producer goroutine fetches
// stripes in order into two frames; the consumer copies bytes out of them.
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
	out      chan *frame // fetched frames, in stripe order
	free     chan *frame // frames the producer may fill
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
		var f *frame
		select {
		case f = <-r.free:
		case <-ctx.Done():
			r.perr = fmt.Errorf("erasure/rs: read %s: %w", r.id, context.Cause(ctx))
			return
		}
		if err := r.fetch(ctx, f, st); err != nil {
			r.free <- f
			r.perr = err
			return
		}
		r.out <- f
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

// drainFrames releases the frames waiting in free. It is used before the
// producer starts, and by Close after it has stopped.
func (r *reader) drainFrames() {
	for {
		select {
		case f := <-r.free:
			f.release()
		default:
			return
		}
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
		r.free <- r.cur
		r.cur = nil
	}
	return n, nil
}

// Close stops the producer, waits for it, and releases the read's frames.
// It is safe to call more than once.
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
	for f := range r.out {
		f.release()
	}
	r.drainFrames()
	return nil
}

// errReaderClosed is returned by Read after Close.
var errReaderClosed = errors.New("erasure/rs: read on closed reader")

package rs

import (
	"context"
	"sync/atomic"

	"github.com/eukalpia/unio/internal/bufpool"
)

// frame holds the blocks of one stripe. Slot i is the block of shard i, and
// all slots share one pooled buffer, so a frame costs (k+m) blocks of budget.
//
// Data block i holds the stripe bytes [i*bl, (i+1)*bl), so the stripe's data
// bytes are the concatenation of the k data blocks truncated to dataLen.
type frame struct {
	buf   *bufpool.Buffer
	slots [][]byte // full-capacity views into buf, one per shard
	// shards are the current views: slots[i][:bl] when shard i holds a block
	// of this stripe, or a zero-length view when it is missing.
	shards  [][]byte
	stripe  int64
	bl      int // block length of this stripe
	dataLen int // data bytes of this stripe
	// refs counts the fan-out consumers that still hold the frame. Only the
	// write and heal paths use it.
	refs atomic.Int32
}

// newFrame takes a frame of n blocks from g's pool, waiting for budget.
func newFrame(ctx context.Context, g *geom, n int) (*frame, error) {
	buf, err := g.frames.Get(ctx)
	if err != nil {
		return nil, err
	}
	all := buf.Bytes()
	f := &frame{buf: buf, slots: make([][]byte, n), shards: make([][]byte, n)}
	for i := range f.slots {
		lo := i * g.block
		f.slots[i] = all[lo : lo+g.block : lo+g.block]
	}
	return f, nil
}

// setStripe sets the stripe index, block length and data length, and points
// every shard at its full block.
func (f *frame) setStripe(stripe int64, bl, dataLen int) {
	f.stripe, f.bl, f.dataLen = stripe, bl, dataLen
	for i, s := range f.slots {
		f.shards[i] = s[:bl]
	}
}

// reset marks every shard as missing until a read or encode sets it.
func (f *frame) reset() {
	for i, s := range f.slots {
		f.shards[i] = s[:0]
	}
}

// release returns the frame's buffer to its pool. It is idempotent.
func (f *frame) release() {
	f.buf.Release()
}

// unref drops one fan-out reference and releases the frame with the last one.
func (f *frame) unref() {
	if f.refs.Add(-1) == 0 {
		f.release()
	}
}

// copyOut copies the stripe's data bytes from off onward into dst and returns
// the number copied. Padding past dataLen is never copied.
func (f *frame) copyOut(dst []byte, off int) int {
	n := 0
	for n < len(dst) {
		pos := off + n
		if pos >= f.dataLen {
			break
		}
		i, inner := pos/f.bl, pos%f.bl
		want := min(len(dst)-n, f.bl-inner, f.dataLen-pos)
		copy(dst[n:n+want], f.shards[i][inner:inner+want])
		n += want
	}
	return n
}

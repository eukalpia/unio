package bitrot

import (
	"errors"
	"fmt"
	"io"

	"github.com/eukalpia/unio/internal/erasure"
)

// Writer frames blocks onto an io.Writer as [digest][data] pairs. The digest
// is bound to the block's position, which advances by one block per call. A
// Writer is not safe for concurrent use. After a write error every later call
// returns the same error.
type Writer struct {
	w    io.Writer
	pos  Position
	next uint64 // block index of the next block
	hdr  [HashSize]byte
	err  error
}

// NewWriter returns a Writer that frames blocks onto w. The first block it
// writes has index pos.FirstBlock.
func NewWriter(w io.Writer, pos Position) *Writer {
	return &Writer{w: w, pos: pos, next: pos.FirstBlock}
}

// WriteBlock writes one framed block containing data. Empty blocks are
// framed like any other block.
func (w *Writer) WriteBlock(data []byte) error {
	if w.err != nil {
		return w.err
	}
	w.hdr = digest(w.pos, w.next, data)
	if _, err := w.w.Write(w.hdr[:]); err != nil {
		w.err = fmt.Errorf("bitrot: write digest: %w", err)
		return w.err
	}
	if _, err := w.w.Write(data); err != nil {
		w.err = fmt.Errorf("bitrot: write block: %w", err)
		return w.err
	}
	w.next++
	return nil
}

// Reader reads and verifies framed blocks from an io.Reader positioned at a
// block boundary. A Reader is not safe for concurrent use.
//
// The caller states the data length of each block, which the framing does
// not record. The Reader expects each block at the position it was given,
// advancing one block index per block, so a block framed for another place
// fails verification. A digest mismatch does not lose alignment, so the next
// block can still be read. Truncation and I/O errors are sticky.
type Reader struct {
	r    io.Reader
	pos  Position
	next uint64 // block index of the next block
	hdr  [HashSize]byte
	err  error
}

// NewReader returns a Reader over r, positioned at the first block of pos.
// The block index of the first block read is pos.FirstBlock; a range read
// must pass the index of its first stripe.
func NewReader(r io.Reader, pos Position) *Reader {
	return &Reader{r: r, pos: pos, next: pos.FirstBlock}
}

// ReadBlock reads the next block into dst, where len(dst) is the block's data
// length, and verifies it against its position. On a mismatch it returns an
// error wrapping erasure.ErrCorrupt. In every error case dst is zeroed, so
// unverified bytes are never returned.
func (r *Reader) ReadBlock(dst []byte) error {
	if r.err != nil {
		clear(dst)
		return r.err
	}
	if _, err := io.ReadFull(r.r, r.hdr[:]); err != nil {
		return r.fail(dst, err)
	}
	if _, err := io.ReadFull(r.r, dst); err != nil {
		return r.fail(dst, err)
	}
	block := r.next
	r.next++
	if digest(r.pos, block, dst) != r.hdr {
		clear(dst)
		return fmt.Errorf("bitrot: block %d of shard %d of %s: digest mismatch: %w",
			block, r.pos.Shard, r.pos.Data, erasure.ErrCorrupt)
	}
	return nil
}

// fail records a read error as sticky, zeroes dst and returns the error.
func (r *Reader) fail(dst []byte, err error) error {
	clear(dst)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		r.err = fmt.Errorf("bitrot: truncated block: %w (%w)", erasure.ErrCorrupt, io.ErrUnexpectedEOF)
	} else {
		r.err = fmt.Errorf("bitrot: read block: %w", err)
	}
	return r.err
}

// Err returns the sticky error that stopped the Reader, or nil.
func (r *Reader) Err() error {
	return r.err
}

// Done verifies that the stream ends exactly after the last block read. Any
// trailing byte means the shard is longer than its framing and is reported
// as corrupt.
func (r *Reader) Done() error {
	if r.err != nil {
		return r.err
	}
	var b [1]byte
	_, err := io.ReadFull(r.r, b[:])
	switch {
	case err == nil:
		r.err = fmt.Errorf("bitrot: trailing data after last block: %w", erasure.ErrCorrupt)
	case errors.Is(err, io.EOF):
		return nil
	default:
		r.err = fmt.Errorf("bitrot: read after last block: %w", err)
	}
	return r.err
}

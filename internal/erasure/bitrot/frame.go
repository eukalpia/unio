package bitrot

import (
	"errors"
	"fmt"
	"io"

	"github.com/zeebo/xxh3"

	"github.com/eukalpia/unio/internal/erasure"
)

// Sum returns the xxh3-128 digest of one block's data, in the byte order used
// by the framing.
func Sum(data []byte) [HashSize]byte {
	return xxh3.Hash128(data).Bytes()
}

// Writer frames blocks onto an io.Writer as [hash][data] pairs. A Writer is
// not safe for concurrent use. After a write error every later call returns
// the same error.
type Writer struct {
	w   io.Writer
	hdr [HashSize]byte
	err error
}

// NewWriter returns a Writer that frames blocks onto w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// WriteBlock writes one framed block containing data. Empty blocks are
// framed like any other block.
func (w *Writer) WriteBlock(data []byte) error {
	if w.err != nil {
		return w.err
	}
	w.hdr = Sum(data)
	if _, err := w.w.Write(w.hdr[:]); err != nil {
		w.err = fmt.Errorf("bitrot: write hash: %w", err)
		return w.err
	}
	if _, err := w.w.Write(data); err != nil {
		w.err = fmt.Errorf("bitrot: write block: %w", err)
		return w.err
	}
	return nil
}

// Reader reads and verifies framed blocks from an io.Reader positioned at a
// block boundary. A Reader is not safe for concurrent use.
//
// The caller states the data length of each block, which the framing does
// not record. A hash mismatch is reported without losing alignment, so the
// next block can still be read. Truncation and I/O errors are sticky.
type Reader struct {
	r   io.Reader
	hdr [HashSize]byte
	err error
}

// NewReader returns a Reader over r, positioned at the first block.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: r}
}

// ReadBlock reads the next block into dst, where len(dst) is the block's data
// length, and verifies it. On a hash mismatch it returns an error wrapping
// erasure.ErrCorrupt. In every error case dst is zeroed, so unverified bytes
// are never returned.
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
	if Sum(dst) != r.hdr {
		clear(dst)
		return fmt.Errorf("bitrot: block hash mismatch: %w", erasure.ErrCorrupt)
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

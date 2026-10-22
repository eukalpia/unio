package engine

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/meta"
)

// errReaderClosed is returned by reads after Close.
var errReaderClosed = errors.New("engine: read from closed object reader")

// segment is the part-local byte range of one erasure-coded part that a read
// needs.
type segment struct {
	set    erasure.Set
	part   meta.PartRecord
	off    int64
	length int64
}

// chainReader concatenates the segments of a window. It opens each part's
// reader only when the previous one is exhausted, and closes it as soon as
// it is exhausted. Any failure is sticky: the reader never returns data after
// an error.
type chainReader struct {
	ctx       context.Context
	segs      []segment
	next      int           // index of the next segment to open
	cur       io.ReadCloser // reader of the current segment, nil if none
	curLeft   int64         // bytes left in the current segment
	remaining int64         // bytes left in the window
	err       error
	closed    bool
}

// Read implements io.Reader.
func (c *chainReader) Read(p []byte) (int, error) {
	switch {
	case c.closed:
		return 0, errReaderClosed
	case c.err != nil:
		return 0, c.err
	case c.remaining == 0:
		return 0, io.EOF
	case len(p) == 0:
		return 0, nil
	}
	for c.curLeft == 0 {
		if err := c.advance(); err != nil {
			c.err = err
			return 0, err
		}
	}
	buf := p
	if int64(len(buf)) > c.curLeft {
		buf = buf[:c.curLeft]
	}
	n, err := c.cur.Read(buf)
	c.curLeft -= int64(n)
	c.remaining -= int64(n)
	switch {
	case err != nil && !errors.Is(err, io.EOF):
		c.err = fmt.Errorf("engine: reading part: %w", err)
		return n, c.err
	case n == 0 && errors.Is(err, io.EOF):
		c.err = fmt.Errorf("engine: part ended early: %w", io.ErrUnexpectedEOF)
		return 0, c.err
	}
	if c.curLeft == 0 {
		if err := c.closeCur(); err != nil {
			c.err = err
			return n, err
		}
	}
	return n, nil
}

// advance closes the current segment and opens the next one.
func (c *chainReader) advance() error {
	if err := c.closeCur(); err != nil {
		return err
	}
	if c.next == len(c.segs) {
		return fmt.Errorf("engine: window exceeds stored data: %w", io.ErrUnexpectedEOF)
	}
	seg := c.segs[c.next]
	c.next++
	rc, err := seg.set.Read(c.ctx, seg.part.Data, seg.part.Layout, seg.part.Size, seg.off, seg.length)
	if err != nil {
		return fmt.Errorf("engine: reading part %d: %w", seg.part.Number, err)
	}
	c.cur = rc
	c.curLeft = seg.length
	return nil
}

func (c *chainReader) closeCur() error {
	if c.cur == nil {
		return nil
	}
	err := c.cur.Close()
	c.cur = nil
	c.curLeft = 0
	if err != nil {
		return fmt.Errorf("engine: closing part reader: %w", err)
	}
	return nil
}

// Close releases the current part reader. It is safe to call more than once.
func (c *chainReader) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	return c.closeCur()
}

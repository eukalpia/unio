package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// content is the byte sequence a read addresses: an inline payload, or the
// ordered parts of an erasure-coded version.
type content struct {
	inline []byte
	parts  []meta.PartRecord
	size   int64
}

// contentOf describes the whole content of a stored version.
func contentOf(rec meta.ObjectRecord) content {
	if rec.Inline != nil {
		return content{inline: rec.Inline, size: int64(len(rec.Inline))}
	}
	return content{parts: rec.Parts, size: rec.Size}
}

// selectPart narrows c to the part with number n, as GetObject with
// PartNumber does. n == 0 selects the whole content. A single-part object
// has the part number 1, and inline objects have no parts other than that.
// A number that names no part of the object is InvalidPartNumber, which S3
// answers with 416.
func selectPart(rec meta.ObjectRecord, c content, n int) (content, error) {
	if n == 0 {
		return c, nil
	}
	if !rec.Multipart {
		if n != 1 {
			return content{}, errNoSuchPart()
		}
		return c, nil
	}
	for _, p := range rec.Parts {
		if p.Number == n {
			return content{parts: []meta.PartRecord{p}, size: p.Size}, nil
		}
	}
	return content{}, errNoSuchPart()
}

// noSuchPartCause is the cause of the InvalidArgument error that a read returns
// for a part number that names no part of the object. The layer reports every
// bad part number as InvalidArgument; the front end declares a local interface
// with NoSuchPart and maps this case to InvalidPartNumber, which S3 answers with
// 416, so that the error does not reveal how many parts the object has.
type noSuchPartCause struct{}

// Error implements error.
func (noSuchPartCause) Error() string {
	return "the object has no part with that number"
}

// NoSuchPart reports that the part number named no part of the object.
func (noSuchPartCause) NoSuchPart() bool {
	return true
}

// errNoSuchPart is the error for a part number that names no part.
func errNoSuchPart() error {
	return s3err.Wrap(s3err.InvalidArgument, noSuchPartCause{})
}

// window resolves an optional range against c. It returns the absolute
// offset and length of the bytes to serve.
func (c content) window(rng *object.HTTPRange) (off, length int64, err error) {
	if rng == nil {
		return 0, c.size, nil
	}
	return rng.Resolve(c.size)
}

// open returns a reader over [off, off+length) of c. Erasure-coded parts are
// read lazily, one after another, and each read is verified by its set.
func (e *Engine) open(ctx context.Context, c content, off, length int64) (io.ReadCloser, error) {
	if c.inline != nil {
		return io.NopCloser(bytes.NewReader(c.inline[off : off+length])), nil
	}
	return e.openParts(ctx, c.parts, off, length)
}

// openParts resolves the sets of the parts that overlap the window. The
// returned reader opens the part readers when it first needs them.
func (e *Engine) openParts(ctx context.Context, parts []meta.PartRecord, off, length int64) (io.ReadCloser, error) {
	end := off + length
	var segs []segment
	var pos int64
	for _, p := range parts {
		start, stop := pos, pos+p.Size
		pos = stop
		lo, hi := max(off, start), min(end, stop)
		if lo >= hi {
			continue
		}
		set, err := e.sets.Get(p.Layout.Set)
		if err != nil {
			return nil, wrapCause(fmt.Sprintf("part %d", p.Number), err)
		}
		segs = append(segs, segment{set: set, part: p, off: lo - start, length: hi - lo})
	}
	return &chainReader{ctx: ctx, segs: segs, remaining: length}, nil
}

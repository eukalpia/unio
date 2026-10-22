package objecttest

import (
	"hash"
	"io"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine/digest"
)

// Body is a test payload that implements object.Body. It computes MD5 and an
// optional checksum over the bytes it hands out, and can inject the failures
// a front end reports while receiving a request.
type Body struct {
	data     []byte
	declared int64
	failAt   int // -1 when no failure is injected
	failErr  error
	finalErr error
	pos      int
	err      error // sticky: returned by every Read after the failure
	md5h     hash.Hash
	alg      object.ChecksumAlgorithm
	ckh      hash.Hash
}

// BodyOption configures a Body.
type BodyOption func(*Body)

// WithChecksum makes the body compute the checksum alg.
func WithChecksum(alg object.ChecksumAlgorithm) BodyOption {
	return func(b *Body) { b.alg = alg }
}

// FailAfter makes Read return err once n bytes were delivered, before the
// end of the payload. Nothing is delivered after the failure.
func FailAfter(n int, err error) BodyOption {
	return func(b *Body) {
		b.failAt = n
		b.failErr = err
	}
}

// FinalError makes the last Read return the remaining bytes together with err
// instead of io.EOF. The body never reports a clean end.
func FinalError(err error) BodyOption {
	return func(b *Body) { b.finalErr = err }
}

// Declared overrides the size the body reports. A size larger than the data
// makes the body fail with io.ErrUnexpectedEOF at the end; a smaller one ends
// the payload early.
func Declared(size int64) BodyOption {
	return func(b *Body) { b.declared = size }
}

// NewBody returns a body over data.
func NewBody(data []byte, opts ...BodyOption) *Body {
	b := &Body{data: data, declared: int64(len(data)), failAt: -1, md5h: digest.NewMD5()}
	for _, o := range opts {
		o(b)
	}
	if b.alg != object.ChecksumNone {
		h, err := digest.New(b.alg)
		if err != nil {
			panic(err) // programming error in a test
		}
		b.ckh = h
	}
	return b
}

// Size implements object.Body.
func (b *Body) Size() int64 { return b.declared }

// MD5 implements object.Body. It is valid after the payload ended cleanly.
func (b *Body) MD5() [16]byte {
	var out [16]byte
	copy(out[:], b.md5h.Sum(nil))
	return out
}

// Checksum implements object.Body.
func (b *Body) Checksum() object.Checksum {
	if b.ckh == nil {
		return object.Checksum{}
	}
	return object.Checksum{Algorithm: b.alg, Type: object.ChecksumFullObject, Value: b.ckh.Sum(nil)}
}

// Read implements io.Reader following the object.Body contract: exactly
// Size bytes, then io.EOF, or an error that replaces io.EOF.
func (b *Body) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.failAt >= 0 && b.pos >= b.failAt {
		b.err = b.failErr
		return 0, b.err
	}
	end := len(b.data)
	if int64(end) > b.declared {
		end = int(b.declared)
	}
	stop := end
	if b.failAt >= 0 && b.failAt < stop {
		stop = b.failAt
	}
	n := copy(p, b.data[b.pos:stop])
	b.pos += n
	b.feed(p[:n])
	switch {
	case b.pos < stop:
		return n, nil
	case b.failAt >= 0 && stop == b.failAt:
		return n, nil // the failure is reported by the next Read
	case int64(len(b.data)) < b.declared:
		b.err = io.ErrUnexpectedEOF
		return n, b.err
	case b.finalErr != nil:
		b.err = b.finalErr
		return n, b.err
	default:
		return n, io.EOF
	}
}

func (b *Body) feed(p []byte) {
	b.md5h.Write(p)
	if b.ckh != nil {
		b.ckh.Write(p)
	}
}

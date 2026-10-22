// Package payload verifies a decoded request payload in one pass and exposes
// it as an object.Body.
//
// Every accepted byte is written once to each active hasher. Read reports
// io.EOF only after the declared length and every requested digest were
// verified, so a consumer that stops at io.EOF may commit the payload.
package payload

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// Body is a verified payload. It implements object.Body.
//
// Read yields exactly Spec.Size bytes and then io.EOF, or an error when a check
// fails. The first error is sticky and returned by every later Read. MD5 and
// Checksum report the digests of the bytes accepted so far; they are final only
// after Read returned io.EOF.
//
// A Body is not safe for concurrent use.
type Body struct {
	r         io.Reader
	size      int64 // declared payload length
	delivered int64 // bytes accepted so far
	err       error // sticky result: io.EOF on success, otherwise the failure

	md5h       hash.Hash
	sha256h    hash.Hash // nil unless Spec.SHA256 is set
	algoh      hash.Hash // nil when no checksum algorithm is requested
	algo       object.ChecksumAlgorithm
	wantMD5    []byte
	wantSHA256 []byte
	wantAlgo   []byte // Spec.Expected
	trailer    func() (object.Checksum, bool)

	// scratch receives digests produced by hash.Hash.Sum, so that computing a
	// digest never allocates.
	scratch [sha256.Size]byte
}

var _ object.Body = (*Body)(nil)

// New validates spec and returns a Body that reads and verifies r. The slices
// in spec are copied, so the caller may reuse them after New returns.
//
// New returns an error when r is nil or spec is inconsistent. These errors
// report programming mistakes; client-supplied header values must be checked
// by the caller before they are passed on.
func New(r io.Reader, spec Spec) (*Body, error) {
	if r == nil {
		return nil, errors.New("payload: nil reader")
	}
	if err := spec.validate(); err != nil {
		return nil, err
	}
	b := &Body{
		r:       r,
		size:    spec.Size,
		md5h:    md5.New(),
		wantMD5: bytes.Clone(spec.ContentMD5),
		algo:    spec.Algorithm,
		trailer: spec.Trailer,
	}
	if spec.SHA256 != nil {
		b.sha256h = sha256.New()
		b.wantSHA256 = bytes.Clone(spec.SHA256)
	}
	if spec.Algorithm != object.ChecksumNone {
		b.algoh = newHash(spec.Algorithm)
		b.wantAlgo = bytes.Clone(spec.Expected)
	}
	return b, nil
}

// Read implements io.Reader with the object.Body contract. It never returns
// more than Size bytes in total. A zero-length p returns 0, nil without
// verifying anything, unless the Body already finished or failed.
//
// Read asks the underlying reader for at most one byte beyond the declared
// remainder, so excess data is detected without reading further. After the
// declared length has been delivered, Read keeps reading the underlying reader
// until it reports io.EOF. Only then are the digests verified, and io.EOF is
// returned if they all match.
func (b *Body) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	remaining := b.size - b.delivered
	if int64(len(p))-1 > remaining {
		p = p[:int(remaining)+1]
	}
	n, err := b.r.Read(p)
	if n < 0 || n > len(p) {
		n, err = 0, fmt.Errorf("payload: reader returned invalid count %d", n)
	}
	if int64(n) > remaining {
		return b.fail(0, s3err.Errorf(s3err.InvalidRequest, "request body is longer than the declared length"))
	}
	if n > 0 {
		b.accept(p[:n])
	}
	switch {
	case err == nil:
		return n, nil
	case err == io.EOF:
		return b.finish(n)
	case s3err.CodeOf(err) == s3err.InternalError:
		return b.fail(n, s3err.Wrap(s3err.IncompleteBody, err))
	default:
		return b.fail(n, err)
	}
}

// Size returns the declared payload length.
func (b *Body) Size() int64 { return b.size }

// MD5 returns the MD5 digest of the bytes accepted so far. It is final only
// after Read returned io.EOF.
func (b *Body) MD5() [md5.Size]byte {
	var out [md5.Size]byte
	copy(out[:], b.digest(b.md5h))
	return out
}

// Checksum returns the requested additional checksum of the bytes accepted so
// far, as a full-object checksum. It is final only after Read returned io.EOF.
// Without a requested algorithm it returns the zero Checksum. The returned
// Value is a copy.
func (b *Body) Checksum() object.Checksum {
	if b.algo == object.ChecksumNone {
		return object.Checksum{}
	}
	return object.Checksum{
		Algorithm: b.algo,
		Type:      object.ChecksumFullObject,
		Value:     bytes.Clone(b.digest(b.algoh)),
	}
}

// accept counts p, which lies within the declared length, and hashes it. The
// hashers never fail to write.
func (b *Body) accept(p []byte) {
	b.delivered += int64(len(p))
	b.md5h.Write(p)
	if b.sha256h != nil {
		b.sha256h.Write(p)
	}
	if b.algoh != nil {
		b.algoh.Write(p)
	}
}

// finish handles io.EOF from the underlying reader, which follows n final
// bytes. It returns io.EOF only when the length matches and verification passes.
func (b *Body) finish(n int) (int, error) {
	if b.delivered != b.size {
		return b.fail(n, s3err.Errorf(s3err.IncompleteBody, "request body is shorter than the declared length"))
	}
	if err := b.verify(); err != nil {
		return b.fail(n, err)
	}
	b.err = io.EOF
	return n, io.EOF
}

// fail makes err the sticky result of the Body and returns it with n.
func (b *Body) fail(n int, err error) (int, error) {
	b.err = err
	return n, err
}

// digest returns the current digest of h. The result aliases b.scratch and is
// valid until the next call to digest.
func (b *Body) digest(h hash.Hash) []byte {
	return h.Sum(b.scratch[:0])
}

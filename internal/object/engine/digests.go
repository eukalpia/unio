package engine

import (
	"hash"
	"io"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine/digest"
)

// digests computes the MD5 and, when requested, the additional checksum of
// data that streams through a writer. Copies use it to produce the ETag and
// checksum of the destination while the bytes go to the erasure set.
type digests struct {
	md5 hash.Hash
	ck  hash.Hash // nil when no checksum was requested
	alg object.ChecksumAlgorithm
}

func newDigests(alg object.ChecksumAlgorithm) (*digests, error) {
	d := &digests{md5: digest.NewMD5(), alg: alg}
	if alg == object.ChecksumNone {
		return d, nil
	}
	h, err := digest.New(alg)
	if err != nil {
		return nil, err
	}
	d.ck = h
	return d, nil
}

// writer returns the writer that feeds every digest.
func (d *digests) writer() io.Writer {
	if d.ck == nil {
		return d.md5
	}
	return io.MultiWriter(d.md5, d.ck)
}

// md5Sum returns the MD5 of everything written so far.
func (d *digests) md5Sum() [16]byte {
	var out [16]byte
	copy(out[:], d.md5.Sum(nil))
	return out
}

// checksum returns the full-object checksum of everything written so far, or a
// zero checksum when none was requested.
func (d *digests) checksum() object.Checksum {
	if d.ck == nil {
		return object.Checksum{}
	}
	return object.Checksum{Algorithm: d.alg, Type: object.ChecksumFullObject, Value: d.ck.Sum(nil)}
}

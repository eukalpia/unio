package payload

import (
	"crypto/md5"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/eukalpia/unio/internal/object"
)

// Spec describes what to verify for one payload.
type Spec struct {
	// Size is the exact decoded payload length (>= 0). Required.
	Size int64
	// ContentMD5 is the decoded Content-MD5 (16 bytes) or nil.
	ContentMD5 []byte
	// SHA256 is the expected SHA-256 of the payload (32 bytes) when the client signed it
	// (auth.Result.PayloadSHA256), else nil.
	SHA256 []byte
	// Algorithm is the requested additional checksum (object.ChecksumNone if none). It
	// must be known before reading starts, because the bytes are hashed while they are read.
	Algorithm object.ChecksumAlgorithm
	// Expected is the digest from an x-amz-checksum-<algo> header (raw bytes) or nil.
	// Its length must match Algorithm.
	Expected []byte
	// Trailer is auth.Result.Trailer: returns the trailing checksum of an aws-chunked body.
	// It is valid only after the underlying reader returned io.EOF. May be nil.
	Trailer func() (object.Checksum, bool)
	// TrailerRequired is set when the request declared a trailing checksum, so the body is
	// only valid if the trailer arrives and names the same algorithm as Algorithm. Requires
	// a non-none Algorithm.
	TrailerRequired bool
}

// validate reports whether s is internally consistent. Its errors describe
// programming mistakes in the caller, not client-visible S3 errors.
func (s Spec) validate() error {
	if s.Size < 0 {
		return fmt.Errorf("payload: negative size %d", s.Size)
	}
	digest, ok := digestLength(s.Algorithm)
	if !ok {
		return fmt.Errorf("payload: unknown checksum algorithm %d", s.Algorithm)
	}
	if s.ContentMD5 != nil && len(s.ContentMD5) != md5.Size {
		return fmt.Errorf("payload: Content-MD5 is %d bytes, want %d", len(s.ContentMD5), md5.Size)
	}
	if s.SHA256 != nil && len(s.SHA256) != sha256.Size {
		return fmt.Errorf("payload: SHA-256 is %d bytes, want %d", len(s.SHA256), sha256.Size)
	}
	if s.TrailerRequired && s.Algorithm == object.ChecksumNone {
		return errors.New("payload: a required trailing checksum needs a checksum algorithm")
	}
	if s.Expected != nil {
		if s.Algorithm == object.ChecksumNone {
			return errors.New("payload: expected checksum given without an algorithm")
		}
		if len(s.Expected) != digest {
			return fmt.Errorf("payload: expected %s digest is %d bytes, want %d", s.Algorithm, len(s.Expected), digest)
		}
	}
	return nil
}

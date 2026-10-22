package payload

import (
	"bytes"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// verify runs the checks that follow io.EOF, in order, and returns the first
// failure. Read calls it once per Body, because its outcome is sticky.
func (b *Body) verify() error {
	if b.wantMD5 != nil && !bytes.Equal(b.digest(b.md5h), b.wantMD5) {
		return s3err.Errorf(s3err.BadDigest, "The Content-MD5 you specified did not match what we received.")
	}
	if b.wantSHA256 != nil && !bytes.Equal(b.digest(b.sha256h), b.wantSHA256) {
		return s3err.Errorf(s3err.XAmzContentSHA256Mismatch, "The provided x-amz-content-sha256 does not match what we received.")
	}
	var algoSum []byte
	if b.algoh != nil {
		algoSum = b.digest(b.algoh)
	}
	if b.wantAlgo != nil && !bytes.Equal(algoSum, b.wantAlgo) {
		return s3err.Errorf(s3err.BadDigest, "The %s you specified did not match the calculated checksum.", b.algo)
	}
	return b.verifyTrailer(algoSum)
}

// verifyTrailer checks the trailing checksum of an aws-chunked body, if the
// caller supplied one. algoSum is the computed digest of the requested
// algorithm, or nil when none was requested.
func (b *Body) verifyTrailer(algoSum []byte) error {
	if b.trailer == nil {
		return nil
	}
	c, ok := b.trailer()
	if !ok {
		return nil
	}
	if b.algo == object.ChecksumNone {
		return s3err.Errorf(s3err.InvalidRequest, "a trailing checksum was sent but no checksum algorithm was requested")
	}
	if c.Algorithm != b.algo {
		return s3err.Errorf(s3err.XAmzContentChecksumMismatch, "The trailer checksum algorithm does not match the requested checksum algorithm.")
	}
	if !bytes.Equal(algoSum, c.Value) {
		return s3err.Errorf(s3err.XAmzContentChecksumMismatch, "The trailer checksum did not match what we received.")
	}
	return nil
}

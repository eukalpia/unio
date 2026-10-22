package object

import "io"

// Body is a verified request payload. It is produced by the S3 front end
// and consumed by the Layer.
//
// Contract:
//   - Read yields exactly Size() bytes and then io.EOF.
//   - If any integrity check fails (Content-MD5, x-amz-content-sha256,
//     x-amz-checksum-*, aws-chunked signatures, length), Read returns an
//     error instead of io.EOF. The final Read may return the error together
//     with the last bytes.
//   - MD5 and Checksum are valid only after Read has returned io.EOF.
//   - A body shorter than Size fails with s3err.IncompleteBody; a longer one
//     with s3err.InvalidRequest. A declared trailing checksum that does not
//     arrive is a failure, never a pass.
//
// Consumers must not commit anything that depends on the payload unless
// Read returned io.EOF (invariant I4).
type Body interface {
	io.Reader
	// Size is the exact decoded payload length.
	Size() int64
	// MD5 is the digest of the bytes read; the object ETag for single-part
	// uploads.
	MD5() [16]byte
	// Checksum is the additional checksum computed over the bytes read for
	// the algorithm the client requested, or a zero Checksum.
	Checksum() Checksum
}

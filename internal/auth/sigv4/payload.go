package sigv4

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"

	"github.com/eukalpia/unio/internal/s3err"
)

// The x-amz-content-sha256 values that select a payload mode.
const (
	streamingPayload         = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	streamingPayloadTrailer  = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER"
	streamingUnsignedTrailer = "STREAMING-UNSIGNED-PAYLOAD-TRAILER"
)

// payloadKind is the payload mode a request declares.
type payloadKind uint8

const (
	// payloadUnsigned: the body is not covered by the signature.
	payloadUnsigned payloadKind = iota
	// payloadDigest: the body must hash to the signed SHA-256 digest.
	payloadDigest
	// payloadStreamingSigned: aws-chunked with a signature per chunk.
	payloadStreamingSigned
	// payloadStreamingTrailer: aws-chunked with per-chunk signatures and a
	// signed trailer.
	payloadStreamingTrailer
	// payloadStreamingUnsignedTrailer: aws-chunked with unsigned chunks and
	// a trailing checksum.
	payloadStreamingUnsignedTrailer
)

// payload is the parsed x-amz-content-sha256 header.
type payload struct {
	kind   payloadKind
	digest [sha256.Size]byte
}

// parsePayloadHash interprets the x-amz-content-sha256 value. The value is
// signed verbatim, so it is also used unchanged in the canonical request.
func parsePayloadHash(v string) (payload, error) {
	switch v {
	case unsignedPayload:
		return payload{kind: payloadUnsigned}, nil
	case streamingPayload:
		return payload{kind: payloadStreamingSigned}, nil
	case streamingPayloadTrailer:
		return payload{kind: payloadStreamingTrailer}, nil
	case streamingUnsignedTrailer:
		return payload{kind: payloadStreamingUnsignedTrailer}, nil
	}
	if len(v) != 2*sha256.Size || !allChars(v, isLowerHex) {
		return payload{}, s3err.Errorf(s3err.InvalidArgument,
			"x-amz-content-sha256 must be UNSIGNED-PAYLOAD, a streaming marker, or a lower-case SHA-256 hex digest")
	}
	var p payload
	p.kind = payloadDigest
	if _, err := hex.Decode(p.digest[:], []byte(v)); err != nil {
		return payload{}, s3err.Errorf(s3err.InvalidArgument, "malformed x-amz-content-sha256")
	}
	return p, nil
}

// digestReader hashes the body as it is read and fails with
// XAmzContentSHA256Mismatch at EOF if the digest differs from the signed
// one. The body is delivered before the check, so callers must not commit
// on error, which the PUT path already guarantees.
type digestReader struct {
	r    io.Reader
	h    hash.Hash
	want [sha256.Size]byte
	err  error
}

func newDigestReader(r io.Reader, want [sha256.Size]byte) *digestReader {
	return &digestReader{r: r, h: sha256.New(), want: want}
}

// Read implements io.Reader.
func (d *digestReader) Read(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	n, err := d.r.Read(p)
	d.h.Write(p[:n])
	if err == io.EOF {
		var got [sha256.Size]byte
		d.h.Sum(got[:0])
		if got != d.want {
			d.err = s3err.Errorf(s3err.XAmzContentSHA256Mismatch, "payload does not match x-amz-content-sha256")
		} else {
			d.err = io.EOF
		}
		return n, d.err
	}
	return n, err
}

// emptyDigest is the SHA-256 of an empty payload.
var emptyDigest = sha256.Sum256(nil)

// checkEmptyDigest verifies that a request without a body signed the digest
// of an empty payload.
func checkEmptyDigest(want [sha256.Size]byte) error {
	if !bytes.Equal(want[:], emptyDigest[:]) {
		return s3err.Errorf(s3err.XAmzContentSHA256Mismatch, "payload does not match x-amz-content-sha256")
	}
	return nil
}

package sigv4

import (
	"bytes"
	"crypto/sha256"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// verified is the material a verified signature hands to the body handling.
type verified struct {
	id  auth.Identity
	pl  payload
	t   time.Time
	amz string
	// scope is the credential scope without the access key.
	scope string
	key   [sha256.Size]byte
	// seed is the request signature, which chains the chunk signatures.
	seed [sha256.Size]byte
}

// buildResult builds the Result for a verified request. Unsigned and digest
// bodies are returned as they arrive, the digest one checked at EOF. For
// aws-chunked bodies the result carries a verifying decoder.
func buildResult(req *auth.Request, v verified) (*auth.Result, error) {
	res := &auth.Result{
		Identity:      v.id,
		Body:          req.Body,
		DecodedLength: req.ContentLength,
		SignedTime:    v.t,
	}
	switch v.pl.kind {
	case payloadUnsigned:
	case payloadDigest:
		res.PayloadSHA256 = v.pl.digest[:]
		if req.Body == nil {
			if !bytes.Equal(v.pl.digest[:], emptyDigest[:]) {
				return nil, s3err.Errorf(s3err.XAmzContentSHA256Mismatch, "payload does not match x-amz-content-sha256")
			}
			break
		}
		res.Body = newDigestReader(req.Body, v.pl.digest)
	default:
		if err := buildChunked(req, v, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// buildChunked attaches the aws-chunked decoder to res.
func buildChunked(req *auth.Request, v verified, res *auth.Result) error {
	decoded, err := decodedLength(req.Header)
	if err != nil {
		return err
	}
	declared, err := declaredTrailers(req.Header)
	if err != nil {
		return err
	}
	var body io.Reader = req.Body
	if body == nil {
		body = bytes.NewReader(nil)
	}
	cr := newChunkedReader(body, chunkedConfig{
		kind:       v.pl.kind,
		key:        v.key,
		amzDate:    v.amz,
		scope:      v.scope,
		seed:       v.seed,
		decodedLen: decoded,
		declared:   declared,
	})
	res.Body = cr
	res.DecodedLength = decoded
	res.Trailer = cr.trailerChecksum
	return nil
}

// decodedLength parses x-amz-decoded-content-length, the payload length
// without aws-chunked framing. It must be a plain decimal no larger than an
// object.
func decodedLength(h http.Header) (int64, error) {
	vals := h.Values("X-Amz-Decoded-Content-Length")
	if len(vals) != 1 {
		return 0, s3err.Errorf(s3err.InvalidRequest, "x-amz-decoded-content-length is required once")
	}
	v := vals[0]
	if len(v) == 0 || len(v) > 13 || !allChars(v, isDigit) {
		return 0, s3err.Errorf(s3err.InvalidRequest, "malformed x-amz-decoded-content-length")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n > object.MaxObjectSize {
		return 0, s3err.Errorf(s3err.InvalidRequest, "x-amz-decoded-content-length is out of range")
	}
	return n, nil
}

// declaredTrailers returns the lower-case trailer names announced in
// x-amz-trailer. A missing header means no trailers.
func declaredTrailers(h http.Header) ([]string, error) {
	vals := h.Values("X-Amz-Trailer")
	if len(vals) == 0 {
		return nil, nil
	}
	if len(vals) > 1 {
		return nil, s3err.Errorf(s3err.InvalidRequest, "duplicate x-amz-trailer")
	}
	var names []string
	for part := range strings.SplitSeq(vals[0], ",") {
		name := strings.ToLower(strings.Trim(part, " "))
		if name == "" {
			return nil, s3err.Errorf(s3err.InvalidRequest, "malformed x-amz-trailer")
		}
		names = append(names, name)
	}
	return names, nil
}

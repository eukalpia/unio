package handlers

import (
	"bytes"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3api/payload"
	"github.com/eukalpia/unio/internal/s3api/xmldec"
	"github.com/eukalpia/unio/internal/s3err"
)

// verifiedBody wraps the request payload so that every byte is verified as it
// is read: length, Content-MD5, the signed SHA-256, the requested additional
// checksum and any trailing checksum. A request without a declared length is
// refused, because the payload could then not be bounded.
func (c *Call) verifiedBody(contentMD5 []byte, sum headers.ChecksumRequest) (*payload.Body, error) {
	size := c.Auth.DecodedLength
	if size < 0 {
		return nil, s3err.Errorf(s3err.MissingContentLength, "you must provide the Content-Length HTTP header")
	}
	src := c.Auth.Body
	if src == nil {
		src = bytes.NewReader(nil)
	}
	body, err := payload.New(src, payload.Spec{
		Size:       size,
		ContentMD5: contentMD5,
		SHA256:     c.Auth.PayloadSHA256,
		Algorithm:  sum.Algorithm,
		Expected:   sum.Expected,
		Trailer:    c.Auth.Trailer,
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

// objectBody returns the verified payload of an object write. It checks the
// declared size against limit (EntityTooLarge) before reading anything.
func (c *Call) objectBody(limit int64) (*payload.Body, headers.ChecksumRequest, error) {
	md5, err := headers.ParseContentMD5(c.Header)
	if err != nil {
		return nil, headers.ChecksumRequest{}, err
	}
	sum, err := headers.ParseChecksumRequest(c.Header)
	if err != nil {
		return nil, headers.ChecksumRequest{}, err
	}
	if c.Auth.DecodedLength > limit {
		return nil, sum, s3err.Errorf(s3err.EntityTooLarge, "your proposed upload exceeds the maximum allowed size")
	}
	body, err := c.verifiedBody(md5, sum)
	return body, sum, err
}

// readXML reads a small XML request body in full, verified and bounded by
// object.MaxXMLBodySize. An empty body yields no bytes.
func (c *Call) readXML() ([]byte, error) {
	if c.Auth.DecodedLength > object.MaxXMLBodySize {
		return nil, s3err.Errorf(s3err.MaxMessageLengthExceeded, "the XML request body is too large")
	}
	md5, err := headers.ParseContentMD5(c.Header)
	if err != nil {
		return nil, err
	}
	body, err := c.verifiedBody(md5, headers.ChecksumRequest{})
	if err != nil {
		return nil, err
	}
	return xmldec.ReadBody(body)
}

// hasPayload reports whether the request declares a non-empty body.
func (c *Call) hasPayload() bool {
	return c.Auth.Body != nil && c.Auth.DecodedLength != 0
}

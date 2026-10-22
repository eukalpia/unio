package auth

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/eukalpia/unio/internal/object"
)

// Request is the transport-independent view of an S3 request that
// authentication needs. It is built by the S3 front end.
type Request struct {
	Method string
	Host   string
	// RawPath is the request path exactly as sent (still percent-encoded).
	RawPath  string
	RawQuery string
	// Header uses canonical MIME header keys.
	Header http.Header
	// Body is the raw request body; nil when there is none.
	Body io.Reader
	// ContentLength is the raw body length, or -1 if unknown.
	ContentLength int64
}

// Result is the outcome of authenticating a request.
type Result struct {
	Identity Identity
	// Body is the decoded payload: aws-chunked framing removed and every
	// chunk signature verified as it is read. It returns an error (never
	// io.EOF) if verification fails. nil when the request has no body.
	Body io.Reader
	// DecodedLength is the exact payload length (x-amz-decoded-content-length
	// for aws-chunked, else Content-Length), or -1 if unknown.
	DecodedLength int64
	// PayloadSHA256 is the expected SHA-256 of the payload when the client
	// signed it (x-amz-content-sha256 holds a digest), else nil.
	PayloadSHA256 []byte
	// Trailer, when non-nil, returns the trailing checksum sent after an
	// aws-chunked body. It is valid only after Body returned io.EOF.
	Trailer func() (object.Checksum, bool)
	// SignedTime is the request time from X-Amz-Date or the presigned URL.
	SignedTime time.Time
}

// Authenticator verifies request signatures.
type Authenticator interface {
	// Authenticate verifies SigV4 (header, presigned, streaming variants).
	// Unsigned requests yield an Anonymous identity. Failures are
	// *s3err.Error values with SignatureDoesNotMatch, InvalidAccessKeyId,
	// RequestTimeTooSkewed, AuthorizationHeaderMalformed, ...
	Authenticate(ctx context.Context, req *Request) (*Result, error)
}

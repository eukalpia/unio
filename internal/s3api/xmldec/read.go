package xmldec

import (
	"io"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// ReadBody reads an XML request body of at most object.MaxXMLBodySize bytes.
// A longer body returns MaxMessageLengthExceeded without reading further.
// Errors from r keep their S3 code: an integrity failure detected at EOF
// (BadDigest, XAmzContentChecksumMismatch, ...) is returned unchanged, and an
// unclassified read failure becomes IncompleteBody.
func ReadBody(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, object.MaxXMLBodySize+1))
	if err != nil {
		if s3err.CodeOf(err) == s3err.InternalError {
			return nil, s3err.Wrap(s3err.IncompleteBody, err)
		}
		return nil, err
	}
	if len(data) > object.MaxXMLBodySize {
		return nil, s3err.Errorf(s3err.MaxMessageLengthExceeded, "the XML request body is too large")
	}
	return data, nil
}

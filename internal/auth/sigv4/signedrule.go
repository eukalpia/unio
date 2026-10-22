package sigv4

import (
	"net/http"
	"slices"
	"strings"

	"github.com/eukalpia/unio/internal/s3err"
)

// mustBeSigned reports whether a request header must be covered by the
// signature whenever it is present. lname is the lower-case header name and
// presigned selects the form of the request. The full rule is in doc.go.
//
// Every x-amz-* header is required in both forms. S3 uses them for metadata,
// copy sources, checksums, access control and the payload hash, and an
// unsigned one could be changed in transit without breaking the signature.
//
// The object headers are required only for header auth: Content-MD5,
// Content-Type, Cache-Control, Content-Disposition, Content-Encoding,
// Content-Language and Expires. A presigned URL is generated before the
// request is sent, and a client such as a browser may add these headers
// afterwards, as the standard upload flow does. S3 accepts that. When such a
// header is listed in X-Amz-SignedHeaders, its value is still verified.
//
// The AWS SDKs sign every header they send, except Authorization,
// User-Agent, X-Amzn-Trace-Id, Expect and Transfer-Encoding. Other headers,
// such as Accept-Encoding, are never covered by this rule.
func mustBeSigned(lname string, presigned bool) bool {
	if strings.HasPrefix(lname, "x-amz-") {
		return true
	}
	if presigned {
		return false
	}
	switch lname {
	case "content-md5", "content-type", "cache-control", "content-disposition",
		"content-encoding", "content-language", "expires":
		return true
	}
	return false
}

// requireHeadersSigned fails with AccessDenied when a header that must be
// signed is present in the request but missing from signed, which is the
// sorted lower-case SignedHeaders list. S3 reports this case with the same
// code and message.
func requireHeadersSigned(h http.Header, signed []string, presigned bool) error {
	for name := range h {
		lname := strings.ToLower(name)
		if mustBeSigned(lname, presigned) && !slices.Contains(signed, lname) {
			return s3err.Errorf(s3err.AccessDenied, "There were headers present in the request which were not signed")
		}
	}
	return nil
}

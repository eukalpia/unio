package sigv4

import (
	"net/http"
	"slices"
	"strings"

	"github.com/eukalpia/unio/internal/s3err"
)

// mustBeSigned reports whether a request header must be covered by the
// signature whenever it is present. lname is the lower-case header name.
//
// The rule, applied to every header in the request:
//
//   - every x-amz-* header. S3 uses them for metadata, copy sources,
//     checksums, access control and the payload hash, and an unsigned one
//     could be changed in transit without breaking the signature;
//   - Content-MD5, and the object headers that S3 stores with the object
//     (Content-Type, Cache-Control, Content-Disposition, Content-Encoding,
//     Content-Language, Expires). Content-MD5 is an integrity check, so an
//     unsigned one could be replaced together with the body.
//
// The AWS SDKs sign every header they send, except Authorization,
// User-Agent, X-Amzn-Trace-Id, Expect and Transfer-Encoding, so requests
// from the SDKs meet this rule. Other headers, such as Accept-Encoding or
// headers added by a proxy, are not covered and do not affect the signature.
func mustBeSigned(lname string) bool {
	if strings.HasPrefix(lname, "x-amz-") {
		return true
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
func requireHeadersSigned(h http.Header, signed []string) error {
	for name := range h {
		lname := strings.ToLower(name)
		if mustBeSigned(lname) && !slices.Contains(signed, lname) {
			return s3err.Errorf(s3err.AccessDenied, "There were headers present in the request which were not signed")
		}
	}
	return nil
}

package headers

import (
	"net/http"
	"strings"

	"github.com/eukalpia/unio/internal/s3err"
)

// unsupportedPrefixes are the prefixes of request headers that ask for a
// feature outside the v0.1 scope: Object Lock, ACL grants and website
// redirects. Accepting one of them would silently drop what the client asked
// for, so the request is refused instead.
var unsupportedPrefixes = [...]string{
	"x-amz-object-lock-",
	"x-amz-grant-",
	"x-amz-website-redirect-location",
}

// RejectUnsupported returns NotImplemented when a request carries a header for
// a feature this server does not implement. A canned ACL is accepted only in
// the forms that grant nothing beyond the owner's own access, which is all this
// server does: "private" and "bucket-owner-full-control". Any other canned ACL
// would promise access that is not enforced.
func RejectUnsupported(h http.Header) error {
	for name := range h {
		lower := strings.ToLower(name)
		for _, p := range unsupportedPrefixes {
			if strings.HasPrefix(lower, p) {
				return s3err.Errorf(s3err.NotImplemented, "this request asks for a feature that is not supported by this server")
			}
		}
	}
	if values := h.Values("X-Amz-Acl"); len(values) > 0 {
		for _, v := range values {
			if v != "private" && v != "bucket-owner-full-control" {
				return s3err.Errorf(s3err.NotImplemented, "this request asks for a feature that is not supported by this server")
			}
		}
	}
	return nil
}

// ParseTaggingDirective checks x-amz-tagging-directive. COPY, the default, is
// the only directive this server supports: the tags of the copy are those of
// the source. REPLACE needs the tags of the request to be applied to the copy
// independently of the metadata directive, which the object layer cannot do yet,
// so it is refused rather than applied as COPY. Any other value is
// InvalidArgument.
func ParseTaggingDirective(h http.Header) error {
	v, ok, err := singleHeader(h, "X-Amz-Tagging-Directive", s3err.InvalidArgument)
	if err != nil || !ok {
		return err
	}
	switch v {
	case "COPY":
		return nil
	case "REPLACE":
		return s3err.Errorf(s3err.NotImplemented, "replacing the tags of a copy is not supported by this server")
	}
	return s3err.Errorf(s3err.InvalidArgument, "x-amz-tagging-directive must be COPY or REPLACE")
}

// HasChecksumDigest reports whether the request carries a digest of the object
// in an x-amz-checksum-<algorithm> header. The x-amz-checksum-mode and
// x-amz-checksum-type headers name no digest and are not counted.
func HasChecksumDigest(h http.Header) bool {
	for name := range h {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, checksumHeaderPrefix) {
			continue
		}
		switch lower {
		case "x-amz-checksum-mode", "x-amz-checksum-type", "x-amz-checksum-algorithm":
			continue
		}
		return true
	}
	return false
}

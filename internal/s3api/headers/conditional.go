package headers

import (
	"net/http"
	"strings"
	"time"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// maxETagLen bounds the length of an ETag condition in bytes.
const maxETagLen = 512

// ParseConditions reads If-Match, If-None-Match, If-Modified-Since and
// If-Unmodified-Since. ETags are unquoted with UnquoteETag and "*" is kept
// as-is. A value that lists several ETags returns InvalidArgument, and so do
// an empty ETag and a repeated ETag header. Dates use http.ParseTime; a date
// that does not parse, or a repeated date header, is ignored as RFC 9110
// requires. Times are returned in UTC.
func ParseConditions(h http.Header) (object.Conditions, error) {
	return parseConditions(h, "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since")
}

// ParseCopyConditions reads x-amz-copy-source-if-match,
// x-amz-copy-source-if-none-match, x-amz-copy-source-if-modified-since and
// x-amz-copy-source-if-unmodified-since, with the same rules as
// ParseConditions.
func ParseCopyConditions(h http.Header) (object.Conditions, error) {
	return parseConditions(h,
		"X-Amz-Copy-Source-If-Match",
		"X-Amz-Copy-Source-If-None-Match",
		"X-Amz-Copy-Source-If-Modified-Since",
		"X-Amz-Copy-Source-If-Unmodified-Since",
	)
}

func parseConditions(h http.Header, match, noneMatch, modified, unmodified string) (object.Conditions, error) {
	var c object.Conditions
	var err error
	if c.IfMatch, err = parseConditionETag(h, match); err != nil {
		return object.Conditions{}, err
	}
	if c.IfNoneMatch, err = parseConditionETag(h, noneMatch); err != nil {
		return object.Conditions{}, err
	}
	c.IfModifiedSince = parseHTTPDate(h, modified)
	c.IfUnmodifiedSince = parseHTTPDate(h, unmodified)
	return c, nil
}

// parseConditionETag reads one ETag condition. Repeated header lines combine
// into a list of ETags, so they are rejected like a list value.
func parseConditionETag(h http.Header, key string) (string, error) {
	vals := h.Values(key)
	switch len(vals) {
	case 0:
		return "", nil
	case 1:
	default:
		return "", s3err.Errorf(s3err.InvalidArgument, "%s lists several ETags", key)
	}
	etag := UnquoteETag(vals[0])
	if strings.Contains(etag, ",") {
		return "", s3err.Errorf(s3err.InvalidArgument, "%s lists several ETags", key)
	}
	if !validConditionETag(etag) {
		return "", s3err.Errorf(s3err.InvalidArgument, "%s is not a valid ETag", key)
	}
	return etag, nil
}

// validConditionETag reports whether s, once unquoted, is "*" or a bare ETag:
// one to maxETagLen bytes of visible ASCII, excluding the quote and the comma.
func validConditionETag(s string) bool {
	if s == "" || len(s) > maxETagLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '"' || c == ',' || c <= 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

// parseHTTPDate reads a date condition. A value that does not parse, or a
// repeated header, yields the zero time.
func parseHTTPDate(h http.Header, key string) time.Time {
	vals := h.Values(key)
	if len(vals) != 1 {
		return time.Time{}
	}
	t, err := http.ParseTime(vals[0])
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

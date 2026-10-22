package headers

import "strings"

// QuoteETag returns etag wrapped in double quotes. A value that is already
// quoted, optionally with the W/ weak marker, is returned unchanged. The
// etag must not contain double quotes; it is not escaped.
func QuoteETag(etag string) string {
	if isQuotedETag(etag) {
		return etag
	}
	return `"` + etag + `"`
}

// UnquoteETag trims surrounding white space, a leading "W/" weak marker and
// one pair of surrounding double quotes from v.
func UnquoteETag(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	return v
}

// isQuotedETag reports whether s is a quoted string, optionally weak.
func isQuotedETag(s string) bool {
	s = strings.TrimPrefix(s, "W/")
	return len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"'
}

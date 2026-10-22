package headers

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/eukalpia/unio/internal/s3err"
)

// singleHeader returns the value of a header that must not repeat. ok is
// false when the header is absent. A repeated header is rejected with code,
// because the combined value it stands for is ambiguous.
func singleHeader(h http.Header, key string, code s3err.Code) (value string, ok bool, err error) {
	switch vals := h.Values(key); len(vals) {
	case 0:
		return "", false, nil
	case 1:
		return vals[0], true, nil
	default:
		return "", false, s3err.Errorf(code, "%s must not be repeated", key)
	}
}

// hasPrefixFold reports whether s starts with prefix, comparing ASCII letters
// case-insensitively. prefix must be lower case. It does not allocate, which
// keeps header scans cheap.
func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}

// containsControl reports whether s holds a C0 control byte or DEL.
func containsControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// parseDigits parses a non-empty run of ASCII digits that fits in an int64.
// It rejects signs, spaces and any other byte that strconv would accept.
func parseDigits(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// decodeBase64Exact decodes strict standard base64 that yields exactly n
// bytes. Padding and trailing bits must be canonical.
func decodeBase64Exact(v string, n int) ([]byte, bool) {
	if len(v) != base64.StdEncoding.EncodedLen(n) {
		return nil, false
	}
	b, err := base64.StdEncoding.Strict().DecodeString(v)
	if err != nil || len(b) != n {
		return nil, false
	}
	return b, true
}

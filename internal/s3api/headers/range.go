package headers

import (
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

const (
	// maxRangeHeaderLen bounds the length of a Range value in bytes.
	maxRangeHeaderLen = 1024
	// maxRangeSpecs bounds the number of ranges listed in a Range value.
	maxRangeSpecs = 64
)

// ParseRange parses a Range header. It returns nil (serve the whole object)
// when the header is absent, uses a unit other than "bytes", or lists several
// ranges, as S3 does not support multi-range reads. Otherwise it returns one
// range: Start and End (End -1 for an open end), or Suffix > 0 for the last
// Suffix bytes. Malformed values, a zero suffix (bytes=-0) and a last byte
// before the first (bytes=5-2) return InvalidRange. Every listed range is
// syntax-checked, even when the result is nil. Overflow of int64 is
// InvalidRange.
func ParseRange(v string) (*object.HTTPRange, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	if len(v) > maxRangeHeaderLen {
		return nil, s3err.Errorf(s3err.InvalidRange, "range header is too long")
	}
	unit, set, ok := strings.Cut(v, "=")
	if !ok || !isToken(unit) {
		return nil, s3err.Errorf(s3err.InvalidRange, "range header is malformed")
	}
	if !strings.EqualFold(unit, "bytes") {
		return nil, nil
	}

	var single object.HTTPRange
	count := 0
	rest := set
	for {
		spec, more, found := strings.Cut(rest, ",")
		if spec = strings.TrimSpace(spec); spec != "" {
			count++
			if count > maxRangeSpecs {
				return nil, s3err.Errorf(s3err.InvalidRange, "range header lists too many ranges")
			}
			r, ok := parseByteRangeSpec(spec)
			if !ok {
				return nil, s3err.Errorf(s3err.InvalidRange, "range header is malformed")
			}
			single = r
		}
		if !found {
			break
		}
		rest = more
	}
	switch {
	case count == 0:
		return nil, s3err.Errorf(s3err.InvalidRange, "range header has no range")
	case count > 1:
		return nil, nil
	}
	return &single, nil
}

// ParseCopySourceRange parses x-amz-copy-source-range, which must be exactly
// "bytes=first-last" with both bounds present and first <= last. Empty input
// returns nil, nil. Anything else returns InvalidArgument.
func ParseCopySourceRange(v string) (*object.HTTPRange, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	unit, set, ok := strings.Cut(v, "=")
	if !ok || !strings.EqualFold(unit, "bytes") {
		return nil, s3err.Errorf(s3err.InvalidArgument, "copy source range must be bytes=first-last")
	}
	first, last, ok := strings.Cut(set, "-")
	if !ok || first == "" || last == "" {
		return nil, s3err.Errorf(s3err.InvalidArgument, "copy source range must be bytes=first-last")
	}
	start, okStart := parseDigits(first)
	end, okEnd := parseDigits(last)
	if !okStart || !okEnd || end < start {
		return nil, s3err.Errorf(s3err.InvalidArgument, "copy source range is invalid")
	}
	return &object.HTTPRange{Start: start, End: end}, nil
}

// parseByteRangeSpec parses one range spec: "first-last", "first-" or
// "-suffix". Suffix specs must be non-zero.
func parseByteRangeSpec(s string) (object.HTTPRange, bool) {
	first, last, ok := strings.Cut(s, "-")
	if !ok {
		return object.HTTPRange{}, false
	}
	switch {
	case first == "" && last != "":
		n, ok := parseDigits(last)
		if !ok || n == 0 {
			return object.HTTPRange{}, false
		}
		return object.HTTPRange{Suffix: n}, true
	case first != "" && last == "":
		start, ok := parseDigits(first)
		if !ok {
			return object.HTTPRange{}, false
		}
		return object.HTTPRange{Start: start, End: -1}, true
	case first != "" && last != "":
		start, okStart := parseDigits(first)
		end, okEnd := parseDigits(last)
		if !okStart || !okEnd || end < start {
			return object.HTTPRange{}, false
		}
		return object.HTTPRange{Start: start, End: end}, true
	}
	return object.HTTPRange{}, false
}

// isToken reports whether s is a non-empty RFC 9110 token.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

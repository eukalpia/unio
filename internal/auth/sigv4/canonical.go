package sigv4

import (
	"cmp"
	"net/http"
	"net/textproto"
	"slices"
	"strconv"
	"strings"

	"github.com/eukalpia/unio/internal/s3err"
)

const upperHex = "0123456789ABCDEF"

// isUnreserved reports whether c is an RFC 3986 unreserved character. These
// are the only bytes SigV4 leaves unescaped.
func isUnreserved(c byte) bool {
	switch {
	case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9':
		return true
	case c == '-', c == '_', c == '.', c == '~':
		return true
	}
	return false
}

// appendEscaped appends s to dst, escaping every byte outside the unreserved
// set as %XX with uppercase hex. keepSlash leaves '/' unescaped, which is
// the rule for paths.
func appendEscaped(dst []byte, s string, keepSlash bool) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) || (keepSlash && c == '/') {
			dst = append(dst, c)
			continue
		}
		dst = append(dst, '%', upperHex[c>>4], upperHex[c&0x0f])
	}
	return dst
}

// unhex returns the value of a hexadecimal digit.
func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// unescape decodes %XX escapes. Unlike url.QueryUnescape it leaves '+'
// alone, because SigV4 encodes spaces as %20. A malformed escape is an
// error.
func unescape(s string) (string, error) {
	if strings.IndexByte(s, '%') < 0 {
		return s, nil
	}
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			buf = append(buf, c)
			continue
		}
		if i+2 >= len(s) {
			return "", errBadEscape
		}
		hi, okHi := unhex(s[i+1])
		lo, okLo := unhex(s[i+2])
		if !okHi || !okLo {
			return "", errBadEscape
		}
		buf = append(buf, hi<<4|lo)
		i += 2
	}
	return string(buf), nil
}

// canonicalURI returns the canonical URI for S3. The path is decoded once
// and encoded once with the S3 rules: S3 does not double-encode, unlike
// other AWS services. Decoding first makes the result independent of how
// the client chose to escape unreserved characters.
func canonicalURI(rawPath string) (string, error) {
	if rawPath == "" {
		return "/", nil
	}
	dec, err := unescape(rawPath)
	if err != nil {
		return "", err
	}
	return string(appendEscaped(make([]byte, 0, len(dec)+8), dec, true)), nil
}

// queryParam is one decoded query-string parameter.
type queryParam struct {
	key, value string
}

// parseQuery splits a raw query string into decoded parameters, keeping
// repeated keys. Malformed escapes are an error.
func parseQuery(raw string) ([]queryParam, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, "&")
	params := make([]queryParam, 0, len(parts))
	for _, part := range parts {
		k, v, _ := strings.Cut(part, "=")
		dk, err := unescape(k)
		if err != nil {
			return nil, err
		}
		dv, err := unescape(v)
		if err != nil {
			return nil, err
		}
		params = append(params, queryParam{key: dk, value: dv})
	}
	return params, nil
}

// canonicalQuery returns the canonical query string: every key and value
// is encoded (with '/' escaped too), pairs are sorted by key and then by
// value, and the parameter named exclude (if any) is omitted.
func canonicalQuery(params []queryParam, exclude string) string {
	type pair struct{ k, v string }
	pairs := make([]pair, 0, len(params))
	for _, p := range params {
		if exclude != "" && p.key == exclude {
			continue
		}
		pairs = append(pairs, pair{
			k: string(appendEscaped(nil, p.key, false)),
			v: string(appendEscaped(nil, p.value, false)),
		})
	}
	slices.SortFunc(pairs, func(a, b pair) int {
		if c := cmp.Compare(a.k, b.k); c != 0 {
			return c
		}
		return cmp.Compare(a.v, b.v)
	})
	var sb strings.Builder
	for i, p := range pairs {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(p.k)
		sb.WriteByte('=')
		sb.WriteString(p.v)
	}
	return sb.String()
}

// normalizeValue trims spaces around a header value and collapses internal
// runs of spaces to one, as SigV4 requires.
func normalizeValue(s string) string {
	s = strings.Trim(s, " ")
	if !strings.Contains(s, "  ") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	space := false
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			if !space {
				sb.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// canonicalSpec gathers everything the canonical request depends on.
type canonicalSpec struct {
	Method string
	// RawPath is the request path as sent.
	RawPath string
	// Query holds the decoded query parameters of the request.
	Query []queryParam
	// ExcludeQuery names a parameter left out of the canonical query, the
	// signature of a presigned URL. Empty for header-signed requests.
	ExcludeQuery string
	// SignedHeaders is the sorted, lower-case list from the signature.
	SignedHeaders []string
	// Header holds the request headers under canonical MIME keys.
	Header http.Header
	// Host is the request Host, used for the "host" signed header.
	Host string
	// ContentLength is used for a signed content-length header that the
	// header map does not carry; -1 when unknown.
	ContentLength int64
	// PayloadHash is the value placed on the last line.
	PayloadHash string
}

// canonicalRequest builds the SigV4 canonical request string. It fails with
// SignatureDoesNotMatch when a signed header is absent, because the client
// could not have signed it.
func canonicalRequest(c canonicalSpec) (string, error) {
	uri, err := canonicalURI(c.RawPath)
	if err != nil {
		return "", s3err.Wrap(s3err.SignatureDoesNotMatch, err)
	}
	var sb strings.Builder
	sb.Grow(256 + len(uri))
	sb.WriteString(c.Method)
	sb.WriteByte('\n')
	sb.WriteString(uri)
	sb.WriteByte('\n')
	sb.WriteString(canonicalQuery(c.Query, c.ExcludeQuery))
	sb.WriteByte('\n')
	for _, name := range c.SignedHeaders {
		value, err := headerValue(c, name)
		if err != nil {
			return "", err
		}
		sb.WriteString(name)
		sb.WriteByte(':')
		sb.WriteString(value)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	sb.WriteString(strings.Join(c.SignedHeaders, ";"))
	sb.WriteByte('\n')
	sb.WriteString(c.PayloadHash)
	return sb.String(), nil
}

// headerValue returns the canonical value of one signed header: the host
// header comes from the request Host, and multiple values are joined with
// commas.
func headerValue(c canonicalSpec, name string) (string, error) {
	if name == "host" {
		return normalizeValue(c.Host), nil
	}
	vals := c.Header.Values(textproto.CanonicalMIMEHeaderKey(name))
	if len(vals) == 0 {
		if name == "content-length" && c.ContentLength >= 0 {
			return strconv.FormatInt(c.ContentLength, 10), nil
		}
		return "", s3err.Errorf(s3err.SignatureDoesNotMatch, "signed header %s is missing", name)
	}
	if len(vals) == 1 {
		return normalizeValue(vals[0]), nil
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = normalizeValue(v)
	}
	return strings.Join(parts, ","), nil
}

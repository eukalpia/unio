package sigv4

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// maxAuthorizationLen bounds the Authorization header. A real value is about
// 250 bytes plus the signed header list.
const maxAuthorizationLen = 4096

// maxSignedHeaders bounds the number of signed headers.
const maxSignedHeaders = 64

// credentialScope is a parsed Credential value:
// <access key>/<date>/<region>/<service>/aws4_request.
type credentialScope struct {
	accessKey string
	date      string // YYYYMMDD
	region    string
	service   string
}

// String returns the scope without the access key, as used in the string to
// sign: date/region/service/aws4_request.
func (s credentialScope) String() string {
	return s.date + "/" + s.region + "/" + s.service + "/" + terminator
}

// authorization is the signature material shared by the header and
// presigned forms.
type authorization struct {
	scope         credentialScope
	signedHeaders []string
	signature     [sha256.Size]byte
}

// parseAuthorization strictly parses an Authorization header:
//
//	AWS4-HMAC-SHA256 Credential=..., SignedHeaders=..., Signature=...
//
// Each of the three parameters must appear exactly once, in any order, and
// nothing else may appear. Every deviation is AuthorizationHeaderMalformed.
func parseAuthorization(v string) (authorization, error) {
	if len(v) > maxAuthorizationLen {
		return authorization{}, malformed("authorization header too long")
	}
	prefix := algorithm + " "
	if !strings.HasPrefix(v, prefix) {
		return authorization{}, malformed("unsupported authorization scheme")
	}
	var credential, signedHeaders, signature string
	var seen [3]bool
	for part := range strings.SplitSeq(v[len(prefix):], ",") {
		part = strings.Trim(part, " ")
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			return authorization{}, malformed("malformed authorization parameter")
		}
		var idx int
		switch name {
		case "Credential":
			idx, credential = 0, value
		case "SignedHeaders":
			idx, signedHeaders = 1, value
		case "Signature":
			idx, signature = 2, value
		default:
			return authorization{}, malformed("unknown authorization parameter")
		}
		if seen[idx] {
			return authorization{}, malformed("duplicate authorization parameter")
		}
		seen[idx] = true
	}
	if !seen[0] || !seen[1] || !seen[2] {
		return authorization{}, malformed("authorization header is missing a parameter")
	}
	return buildAuthorization(credential, signedHeaders, signature)
}

// buildAuthorization parses the three raw parameter values shared by the
// header and presigned forms.
func buildAuthorization(credential, signedHeaders, signature string) (authorization, error) {
	scope, err := parseCredential(credential)
	if err != nil {
		return authorization{}, err
	}
	names, err := parseSignedHeaders(signedHeaders)
	if err != nil {
		return authorization{}, err
	}
	sig, err := parseSignature(signature)
	if err != nil {
		return authorization{}, err
	}
	return authorization{scope: scope, signedHeaders: names, signature: sig}, nil
}

// parseCredential parses and validates a credential scope. The access key
// may contain only letters and digits, so it can never be confused with the
// '/' separators.
func parseCredential(v string) (credentialScope, error) {
	parts := strings.Split(v, "/")
	if len(parts) != 5 {
		return credentialScope{}, malformed("malformed Credential")
	}
	ak := parts[0]
	if len(ak) < 1 || len(ak) > 128 || !allChars(ak, isAlnum) {
		return credentialScope{}, malformed("malformed access key in Credential")
	}
	if _, err := time.Parse(dateLayout, parts[1]); err != nil || len(parts[1]) != 8 {
		return credentialScope{}, malformed("malformed date in Credential")
	}
	region := parts[2]
	if len(region) < 1 || len(region) > 64 || !allChars(region, isRegionChar) {
		return credentialScope{}, malformed("malformed region in Credential")
	}
	if parts[3] != service {
		return credentialScope{}, malformed("Credential service must be s3")
	}
	if parts[4] != terminator {
		return credentialScope{}, malformed("Credential must end in aws4_request")
	}
	return credentialScope{accessKey: ak, date: parts[1], region: region, service: parts[3]}, nil
}

// parseSignedHeaders parses a semicolon-separated list of lower-case header
// names. The list must be sorted, without duplicates, and must contain host.
func parseSignedHeaders(v string) ([]string, error) {
	if v == "" {
		return nil, malformed("empty SignedHeaders")
	}
	names := strings.Split(v, ";")
	if len(names) > maxSignedHeaders {
		return nil, malformed("too many signed headers")
	}
	hasHost := false
	for i, n := range names {
		if n == "" || !allChars(n, isHeaderNameChar) {
			return nil, malformed("malformed signed header name")
		}
		if i > 0 && n <= names[i-1] {
			return nil, malformed("SignedHeaders must be sorted and unique")
		}
		if n == "host" {
			hasHost = true
		}
	}
	if !hasHost {
		return nil, malformed("host must be a signed header")
	}
	return names, nil
}

// parseSignature decodes a 64-character lower-case hex signature.
func parseSignature(v string) ([sha256.Size]byte, error) {
	var out [sha256.Size]byte
	if len(v) != 2*sha256.Size || !allChars(v, isLowerHex) {
		return out, malformed("malformed Signature")
	}
	if _, err := hex.Decode(out[:], []byte(v)); err != nil {
		return out, malformed("malformed Signature")
	}
	return out, nil
}

func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

func isRegionChar(c byte) bool {
	return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-'
}

func isHeaderNameChar(c byte) bool {
	return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.'
}

func isLowerHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f'
}

// allChars reports whether every byte of s satisfies ok.
func allChars(s string, ok func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

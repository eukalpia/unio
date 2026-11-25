package s3api

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
)

// bearerScheme is the lower-case name of the Bearer authentication scheme.
const bearerScheme = "bearer"

// tokenDigest is the SHA-256 digest of the metrics token. The server keeps the
// digest instead of the token, so the secret is hashed once, in New, and never
// compared byte by byte.
type tokenDigest struct {
	set    bool
	digest [sha256.Size]byte
}

// newTokenDigest returns the digest of token. An empty token leaves the digest
// unset, which disables the bearer path.
func newTokenDigest(token []byte) tokenDigest {
	if len(token) == 0 {
		return tokenDigest{}
	}
	return tokenDigest{set: true, digest: sha256.Sum256(token)}
}

// matches reports whether presented is the configured token. Both values are
// hashed first, so the comparison works on two digests of the same length and
// the length of presented is not revealed by the time it takes. An unset
// digest matches nothing.
func (t tokenDigest) matches(presented []byte) bool {
	if !t.set {
		return false
	}
	d := sha256.Sum256(presented)
	return subtle.ConstantTimeCompare(d[:], t.digest[:]) == 1
}

// parseBearer reports whether an Authorization header value uses the Bearer
// scheme, and returns the token it presents. The scheme name is matched
// without regard to case, as RFC 9110 section 11.1 requires. A value that
// begins with the scheme name but has no space after it is still a Bearer
// attempt, with an empty token, so that no Bearer header can reach the SigV4
// path.
func parseBearer(authorization []byte) (token []byte, bearer bool) {
	if !hasBearerScheme(authorization) {
		return nil, false
	}
	rest := authorization[len(bearerScheme):]
	if len(rest) == 0 || rest[0] != ' ' {
		return nil, true
	}
	return bytes.TrimLeft(rest, " "), true
}

// hasBearerScheme reports whether v begins with the scheme name, compared
// byte by byte with ASCII case folding. Unicode folding is not applied.
func hasBearerScheme(v []byte) bool {
	if len(v) < len(bearerScheme) {
		return false
	}
	for i := 0; i < len(bearerScheme); i++ {
		c := v[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != bearerScheme[i] {
			return false
		}
	}
	return true
}

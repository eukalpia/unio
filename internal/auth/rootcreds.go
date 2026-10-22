package auth

import "github.com/eukalpia/unio/internal/s3err"

// Bounds on the root credentials. The node refuses to start with credentials
// outside these bounds, and configuration validation applies the same rules.
const (
	minRootAccessKeyLen = 12
	maxAccessKeyIDLen   = 128
	minRootSecretLen    = 32
	maxSecretLen        = 256
	// minDistinctSecretChars rejects secrets such as "aaaa...".
	minDistinctSecretChars = 10
)

// ValidAccessKeyID reports whether id has the shape of an access key ID:
// letters and digits, between 12 and 128 characters.
func ValidAccessKeyID(id string) bool {
	if len(id) < minRootAccessKeyLen || len(id) > maxAccessKeyIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		if !isAlnum(id[i]) {
			return false
		}
	}
	return true
}

// ValidateRootCredentials checks root credentials from configuration. The
// access key must be 12 or more letters and digits. The secret must be 32 to
// 256 printable characters, without spaces, and use at least ten distinct
// characters. Weak credentials are refused at startup, so the cluster never
// runs with a guessable root key.
func ValidateRootCredentials(accessKey, secret string) error {
	if len(accessKey) < minRootAccessKeyLen {
		return weakf("root access key must be at least %d characters", minRootAccessKeyLen)
	}
	if !ValidAccessKeyID(accessKey) {
		return weakf("root access key must contain only letters and digits, at most %d characters", maxAccessKeyIDLen)
	}
	if len(secret) < minRootSecretLen {
		return weakf("root secret key must be at least %d characters", minRootSecretLen)
	}
	if len(secret) > maxSecretLen {
		return weakf("root secret key must be at most %d characters", maxSecretLen)
	}
	for i := 0; i < len(secret); i++ {
		if c := secret[i]; c < 0x21 || c > 0x7e {
			return weakf("root secret key must contain only printable characters without spaces")
		}
	}
	if distinctChars(secret) < minDistinctSecretChars {
		return weakf("root secret key must use at least %d distinct characters", minDistinctSecretChars)
	}
	return nil
}

// isAlnum reports whether c is an ASCII letter or digit.
func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// distinctChars counts the distinct bytes in s.
func distinctChars(s string) int {
	var seen [256]bool
	n := 0
	for i := 0; i < len(s); i++ {
		if !seen[s[i]] {
			seen[s[i]] = true
			n++
		}
	}
	return n
}

// weakf returns the error for a credential that does not meet the policy.
func weakf(format string, args ...any) error {
	return s3err.Errorf(s3err.InvalidArgument, format, args...)
}

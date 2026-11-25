package auth

import (
	"strings"

	"github.com/eukalpia/unio/internal/s3err"
)

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

// wellKnownUserNames are access key IDs that must never be the root user. They
// are compared without regard to case.
var wellKnownUserNames = []string{
	"admin", "administrator", "root", "user", "test", "default", "unio",
	"unioadmin", "minioadmin", "access", "accesskey", "username",
}

// wellKnownSecrets are root secrets that must never be used. They are compared
// without regard to case. Each is shorter than minRootSecretLen, so the length
// rule refuses them first. The check stays so that the rule set is the one
// configuration applied before these rules moved into this package.
var wellKnownSecrets = []string{"secret", "unio", "admin", "root"}

// secretFragments mark a root secret as a template or a documentation example.
// They are matched as substrings, without regard to case.
var secretFragments = []string{
	"changeme", "minioadmin", "replace_with", "placeholder", "example",
}

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

// ValidateRootCredentials checks root credentials from configuration and at
// node start. The access key must be 12 to 128 letters and digits, and it must
// not be a well-known default name. The secret must be 32 to 256 printable
// characters without spaces, with at least ten distinct characters. It must
// differ from the access key, must not contain a template or example fragment
// or the word password, and must not be a well-known default. Weak credentials
// are refused, so the cluster never runs with a guessable root key. The error
// never includes a value.
func ValidateRootCredentials(accessKey, secret string) error {
	if len(accessKey) < minRootAccessKeyLen {
		return weakf("root access key must be at least %d characters", minRootAccessKeyLen)
	}
	if !ValidAccessKeyID(accessKey) {
		return weakf("root access key must contain only letters and digits, at most %d characters", maxAccessKeyIDLen)
	}
	if isWellKnown(accessKey, wellKnownUserNames) {
		return weakf("root access key must not be a well-known default name; choose a random access key ID")
	}
	if len(secret) < minRootSecretLen {
		return weakf("root secret key must be at least %d characters", minRootSecretLen)
	}
	if len(secret) > maxSecretLen {
		return weakf("root secret key must be at most %d characters", maxSecretLen)
	}
	if !printableNoSpace(secret) {
		return weakf("root secret key must contain only printable characters without spaces")
	}
	if distinctChars(secret) < minDistinctSecretChars {
		return weakf("root secret key must use at least %d distinct characters", minDistinctSecretChars)
	}
	if secret == accessKey {
		return weakf("root secret key must differ from the root access key")
	}
	if containsFragment(secret) {
		return weakf("root secret key must not be a template or documentation example; generate a random value")
	}
	if strings.Contains(strings.ToLower(secret), "password") {
		return weakf("root secret key must not contain the word password; generate a random value")
	}
	if isWellKnown(secret, wellKnownSecrets) {
		return weakf("root secret key must not be a well-known default; generate a random value")
	}
	return nil
}

// isAlnum reports whether c is an ASCII letter or digit.
func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// printableNoSpace reports whether every byte of s is printable ASCII other than a space.
func printableNoSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
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

// isWellKnown reports whether value equals, without regard to case, one of words.
func isWellKnown(value string, words []string) bool {
	lower := strings.ToLower(value)
	for _, w := range words {
		if lower == w {
			return true
		}
	}
	return false
}

// containsFragment reports whether value contains a template or example
// fragment, without regard to case.
func containsFragment(value string) bool {
	lower := strings.ToLower(value)
	for _, f := range secretFragments {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}

// weakf returns the error for a credential that does not meet the policy.
func weakf(format string, args ...any) error {
	return s3err.Errorf(s3err.InvalidArgument, format, args...)
}

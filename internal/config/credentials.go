package config

import (
	"fmt"
	"strings"
)

// Strength requirements for credentials. Unio has no default credentials, and
// these minimums are enforced everywhere a credential is accepted.
const (
	// MinRootUserLength is the minimum length of the root access key ID.
	MinRootUserLength = 12
	// MinRootPasswordLength is the minimum length of the root secret key.
	MinRootPasswordLength = 32
	// MinSecretLength is the minimum length of the cluster secret and the
	// metrics token.
	MinSecretLength = 32
	// minSecretDistinct is the minimum number of distinct characters in a secret.
	minSecretDistinct = 8
	// minPasswordDistinct is the minimum number of distinct characters in a root password.
	minPasswordDistinct = 10
)

// defaultUserNames are well-known user names that must never be used as a root user.
var defaultUserNames = []string{
	"admin", "administrator", "root", "user", "test", "default", "unio",
	"unioadmin", "minioadmin", "access", "accesskey", "username",
}

// knownBadFragments are substrings that mark a value as a template, a
// documentation example or a well-known default.
var knownBadFragments = []string{
	"changeme", "minioadmin", "replace_with", "placeholder", "example",
}

// CheckRootUser reports whether user is an acceptable root access key ID:
// at least MinRootUserLength printable ASCII characters without spaces, not a
// well-known default. The error never includes the value.
func CheckRootUser(user string) error {
	if len(user) < MinRootUserLength {
		return fmt.Errorf("must be at least %d characters", MinRootUserLength)
	}
	if !printableNoSpace(user) {
		return fmt.Errorf("must contain only printable ASCII characters without spaces")
	}
	if isWellKnown(user, defaultUserNames) {
		return fmt.Errorf("is a well-known default; choose a random user name (unio init generates one)")
	}
	return nil
}

// CheckRootPassword reports whether password is an acceptable root secret key:
// at least MinRootPasswordLength printable ASCII characters without spaces, at
// least minPasswordDistinct distinct characters, not equal to user and not a
// well-known default. The error never includes the value.
func CheckRootPassword(user, password string) error {
	if len(password) < MinRootPasswordLength {
		return fmt.Errorf("must be at least %d characters", MinRootPasswordLength)
	}
	if !printableNoSpace(password) {
		return fmt.Errorf("must contain only printable ASCII characters without spaces")
	}
	if distinctChars(password) < minPasswordDistinct {
		return fmt.Errorf("must contain at least %d distinct characters", minPasswordDistinct)
	}
	if password == user {
		return fmt.Errorf("must differ from the root user name")
	}
	if containsBadFragment(password) || strings.Contains(strings.ToLower(password), "password") ||
		isWellKnown(password, []string{"secret", "unio", "admin", "root"}) {
		return fmt.Errorf("is a well-known default or template; generate a random value (unio init does)")
	}
	return nil
}

// CheckSecretValue reports whether value is an acceptable cluster secret or
// metrics token: at least MinSecretLength printable characters without spaces
// and at least minSecretDistinct distinct characters. The error never
// includes the value.
func CheckSecretValue(value string) error {
	if len(value) < MinSecretLength {
		return fmt.Errorf("must be at least %d characters", MinSecretLength)
	}
	if !printableNoSpace(value) {
		return fmt.Errorf("must contain only printable ASCII characters without spaces")
	}
	if distinctChars(value) < minSecretDistinct {
		return fmt.Errorf("must contain at least %d distinct characters", minSecretDistinct)
	}
	if containsBadFragment(value) {
		return fmt.Errorf("is a template or documentation example; generate a random value (unio init does)")
	}
	return nil
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

// isWellKnown reports whether value equals (case-insensitively) one of words.
func isWellKnown(value string, words []string) bool {
	lower := strings.ToLower(value)
	for _, w := range words {
		if lower == w {
			return true
		}
	}
	return false
}

// containsBadFragment reports whether value contains a template or example fragment.
func containsBadFragment(value string) bool {
	lower := strings.ToLower(value)
	for _, f := range knownBadFragments {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}

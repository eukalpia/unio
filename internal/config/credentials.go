package config

import (
	"fmt"
	"strings"
)

// Strength requirements for the cluster secret and the metrics token. The root
// credentials are checked by auth.ValidateRootCredentials, the rules the node
// applies at startup.
const (
	// MinSecretLength is the minimum length of the cluster secret and the
	// metrics token.
	MinSecretLength = 32
	// minSecretDistinct is the minimum number of distinct characters in a secret.
	minSecretDistinct = 8
)

// knownBadFragments are substrings that mark a value as a template, a
// documentation example or a well-known default.
var knownBadFragments = []string{
	"changeme", "minioadmin", "replace_with", "placeholder", "example",
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

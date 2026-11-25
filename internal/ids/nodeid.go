package ids

import "fmt"

// maxNodeIDLen is the longest DNS label, so the longest node id.
const maxNodeIDLen = 63

// ValidateNodeID reports whether id is usable as a node identity. A node id is
// a DNS label, because it appears in the certificate's DNS name
// <id>.node.unio. It must be 1 to 63 characters from lowercase ASCII letters,
// digits and hyphens, and it must not begin or end with a hyphen.
func ValidateNodeID(id NodeID) error {
	s := string(id)
	if s == "" || len(s) > maxNodeIDLen {
		return fmt.Errorf("ids: node id must be 1 to %d characters", maxNodeIDLen)
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return fmt.Errorf("ids: node id %q must not begin or end with a hyphen", s)
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return fmt.Errorf("ids: node id %q is not a DNS label: use lowercase letters, digits and hyphens", s)
		}
	}
	return nil
}

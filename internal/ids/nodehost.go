package ids

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
)

// maxDNSNameLen is the longest host name, without a trailing dot.
const maxDNSNameLen = 253

// NodeIDFromHost derives the node id of a host, as written in a drive endpoint.
// The host is trimmed and lowercased first. An IPv4 address a.b.c.d gives
// ip-a-b-c-d. An IPv6 address gives ip6- followed by the 32 hex digits of its
// 16-byte form. Any other host must be a DNS name, and its id is the first
// label, so unio-0.unio-hl.ns.svc.cluster.local gives unio-0. The result always
// passes ValidateNodeID, and an error means the host cannot name a node.
func NodeIDFromHost(host string) (NodeID, error) {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return "", errors.New("host is empty")
	}
	var id NodeID
	switch {
	case strings.Contains(h, ":"):
		ip := net.ParseIP(h)
		if ip == nil {
			return "", fmt.Errorf("host %q is not a valid IPv6 address", host)
		}
		id = NodeID("ip6-" + hex.EncodeToString(ip.To16()))
	case net.ParseIP(h) != nil:
		v4 := net.ParseIP(h).To4()
		id = NodeID(fmt.Sprintf("ip-%d-%d-%d-%d", v4[0], v4[1], v4[2], v4[3]))
	default:
		if err := checkDNSName(h); err != nil {
			return "", fmt.Errorf("host %q: %w", host, err)
		}
		first, _, _ := strings.Cut(h, ".")
		id = NodeID(first)
	}
	if err := ValidateNodeID(id); err != nil {
		return "", fmt.Errorf("host %q: %w", host, err)
	}
	return id, nil
}

// checkDNSName reports whether name is a DNS name of at most 253 characters
// whose labels are 1 to 63 characters from lowercase letters, digits and
// hyphens, with no leading or trailing hyphen in any label.
func checkDNSName(name string) error {
	if len(name) > maxDNSNameLen {
		return fmt.Errorf("name is longer than %d characters", maxDNSNameLen)
	}
	for _, label := range strings.Split(name, ".") {
		switch {
		case label == "":
			return errors.New("name has an empty label (a trailing dot is not allowed)")
		case len(label) > maxNodeIDLen:
			return fmt.Errorf("label %q is longer than %d characters", label, maxNodeIDLen)
		case label[0] == '-' || label[len(label)-1] == '-':
			return fmt.Errorf("label %q must not begin or end with a hyphen", label)
		}
		for i := range len(label) {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return fmt.Errorf("label %q has a character that is not a letter, digit or hyphen", label)
			}
		}
	}
	return nil
}

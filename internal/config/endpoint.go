package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/eukalpia/unio/internal/ids"
)

// Endpoint identifies one drive of the cluster: the node that owns it and
// the absolute path of the drive on that node. The textual form is
// scheme://host:port/path, for example https://node1:9100/data1.
type Endpoint struct {
	// Scheme is "https" in a valid configuration. ParseEndpoint also accepts
	// "http", so that validation can refuse it with an explanation.
	Scheme string
	// Host is the node's host name or IP address, without brackets.
	Host string
	// Port is the internode port of the node.
	Port int
	// Path is the absolute, clean path of the drive.
	Path string
}

// maxEndpointLength bounds the textual length of an endpoint.
const maxEndpointLength = 512

// ParseEndpoint parses and validates one concrete endpoint. It does not expand
// ellipses; use ExpandEndpoints for patterns. Credentials, queries and
// fragments are rejected.
func ParseEndpoint(raw string) (Endpoint, error) {
	if raw == "" || len(raw) > maxEndpointLength {
		return Endpoint{}, fmt.Errorf("endpoint must be 1 to %d characters", maxEndpointLength)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, fmt.Errorf("endpoint %q is not a valid URL: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return Endpoint{}, fmt.Errorf("endpoint %q: scheme must be https", raw)
	}
	if u.User != nil {
		return Endpoint{}, fmt.Errorf("endpoint %q must not contain credentials", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return Endpoint{}, fmt.Errorf("endpoint %q must not contain a query or fragment", raw)
	}
	host := u.Hostname()
	if _, err := ids.NodeIDFromHost(host); err != nil {
		return Endpoint{}, fmt.Errorf("endpoint %q: %w", raw, err)
	}
	portStr := u.Port()
	port, err := strconv.Atoi(portStr)
	if err != nil || portStr == "" || port < 1 || port > 65535 {
		return Endpoint{}, fmt.Errorf("endpoint %q: an explicit port between 1 and 65535 is required", raw)
	}
	if err := validDrivePath(u.Path); err != nil {
		return Endpoint{}, fmt.Errorf("endpoint %q: %w", raw, err)
	}
	return Endpoint{Scheme: scheme, Host: host, Port: port, Path: u.Path}, nil
}

// validDrivePath checks that p is an absolute, clean path that is not the root.
func validDrivePath(p string) error {
	switch {
	case p == "" || p[0] != '/':
		return errors.New("drive path must be absolute, for example /data1")
	case p == "/":
		return errors.New("drive path must not be the root directory")
	case path.Clean(p) != p:
		return fmt.Errorf("drive path %q must be clean (no '.', '..' or repeated or trailing slashes)", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return errors.New("drive path must not contain control characters")
		}
	}
	return nil
}

// NodeAddress returns host:port, with IPv6 hosts bracketed.
func (e Endpoint) NodeAddress() string {
	return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// String returns the canonical textual form of the endpoint.
func (e Endpoint) String() string {
	return e.Scheme + "://" + e.NodeAddress() + e.Path
}

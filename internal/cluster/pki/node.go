package pki

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/eukalpia/unio/internal/ids"
)

const (
	nodeURIScheme = "unio"
	nodeURIHost   = "node"

	// nodeDNSSuffix is the DNS zone that node names live in. A node's
	// certificate names it <id>.node.unio, and a dialer sets ServerName to that
	// name, so the standard hostname check binds the certificate to the node.
	nodeDNSSuffix = "node.unio"

	// maxNodeIDLen is the longest DNS label, so the longest node id.
	maxNodeIDLen = 63
)

// ValidateNodeID reports whether id is usable as a node identity. A node id is
// a DNS label, because it appears in the certificate's DNS name
// <id>.node.unio. It must be 1 to 63 characters from lowercase ASCII letters,
// digits and hyphens, and it must not begin or end with a hyphen.
func ValidateNodeID(id ids.NodeID) error {
	s := string(id)
	if s == "" || len(s) > maxNodeIDLen {
		return fmt.Errorf("pki: node id must be 1 to %d characters", maxNodeIDLen)
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return fmt.Errorf("pki: node id %q must not begin or end with a hyphen", s)
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return fmt.Errorf("pki: node id %q is not a DNS label: use lowercase letters, digits and hyphens", s)
		}
	}
	return nil
}

// NodeDNSName returns the DNS name of node id, the name a dialer sets as
// ServerName and a certificate for that node carries.
func NodeDNSName(id ids.NodeID) (string, error) {
	if err := ValidateNodeID(id); err != nil {
		return "", err
	}
	return string(id) + "." + nodeDNSSuffix, nil
}

// IssueNode generates a fresh Ed25519 key and a one-shot certificate for node
// id, valid for DefaultLifetime. The certificate names the node twice: as the
// URI SAN unio://node/<id>, and as the DNS name <id>.node.unio. It also carries
// the given hosts as DNS or IP SANs. A node that stays up should use an Issuer,
// which renews the certificate before it expires.
func (ca *CA) IssueNode(id ids.NodeID, hosts []string) (tls.Certificate, error) {
	now := time.Now()
	return ca.issueAt(id, hosts, now.Add(-clockSkew), now.Add(DefaultLifetime))
}

// issueAt is IssueNode with explicit validity bounds, for tests.
func (ca *CA) issueAt(id ids.NodeID, hosts []string, notBefore, notAfter time.Time) (tls.Certificate, error) {
	uri, err := nodeURI(id)
	if err != nil {
		return tls.Certificate{}, err
	}
	dnsName, err := NodeDNSName(id)
	if err != nil {
		return tls.Certificate{}, err
	}
	dns, ips, err := splitHosts(hosts)
	if err != nil {
		return tls.Certificate{}, err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("pki: generate node key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: uri.String()},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{uri},
		DNSNames:     append([]string{dnsName}, dns...),
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("pki: sign node certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("pki: parse node certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv, Leaf: leaf}, nil
}

// nodeURI returns the identity URI for id.
func nodeURI(id ids.NodeID) (*url.URL, error) {
	if err := ValidateNodeID(id); err != nil {
		return nil, err
	}
	return &url.URL{Scheme: nodeURIScheme, Host: nodeURIHost, Path: "/" + string(id)}, nil
}

// splitHosts separates host names into DNS names and IP addresses.
func splitHosts(hosts []string) (dns []string, ips []net.IP, err error) {
	for _, h := range hosts {
		switch {
		case h == "" || strings.ContainsAny(h, " \t\r\n/"):
			return nil, nil, fmt.Errorf("pki: invalid host %q", h)
		case net.ParseIP(h) != nil:
			ips = append(ips, net.ParseIP(h))
		default:
			dns = append(dns, h)
		}
	}
	return dns, ips, nil
}

// randomSerial returns a random positive 128-bit serial number.
func randomSerial() (*big.Int, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("pki: serial number: %w", err)
	}
	b[0] &= 0x7f
	b[15] |= 1
	return new(big.Int).SetBytes(b[:]), nil
}

// nodeIDFromURI parses an identity URI, returning false unless it is exactly
// unio://node/<valid id> with no query, fragment, user or opaque part.
func nodeIDFromURI(u *url.URL) (ids.NodeID, bool) {
	if u == nil || u.Scheme != nodeURIScheme || u.Host != nodeURIHost ||
		u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	id := ids.NodeID(u.Path[1:])
	if ValidateNodeID(id) != nil {
		return "", false
	}
	return id, true
}

// nodeIDFromCert returns the node identity carried by cert. The certificate
// must carry exactly one identity URI, and it must also carry the DNS name of
// that node. The two names are issued together, so a certificate in which they
// disagree is refused.
func nodeIDFromCert(cert *x509.Certificate) (ids.NodeID, error) {
	if cert == nil {
		return "", errors.New("pki: no peer certificate")
	}
	var found ids.NodeID
	n := 0
	for _, u := range cert.URIs {
		if id, ok := nodeIDFromURI(u); ok {
			found = id
			n++
		}
	}
	if n != 1 {
		return "", fmt.Errorf("pki: certificate has %d node identity URIs, want 1", n)
	}
	dnsName, err := NodeDNSName(found)
	if err != nil {
		return "", err
	}
	for _, name := range cert.DNSNames {
		if name == dnsName {
			return found, nil
		}
	}
	return "", fmt.Errorf("pki: certificate does not name node %s in DNS as %s", found, dnsName)
}

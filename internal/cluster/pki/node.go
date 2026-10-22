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
	// NodeCertValidity is the validity of a node certificate issued by IssueNode.
	NodeCertValidity = 365 * 24 * time.Hour

	// clockSkew lets a freshly issued certificate be accepted by peers whose
	// clocks run slightly behind.
	clockSkew = time.Minute

	nodeURIScheme = "unio"
	nodeURIHost   = "node"
	maxNodeIDLen  = 64
)

// ValidateNodeID reports whether id is usable as a node identity. Node ids
// appear in certificate URIs, so they are restricted to ASCII letters,
// digits, '.', '_' and '-', with at most 64 characters.
func ValidateNodeID(id ids.NodeID) error {
	s := string(id)
	if s == "" || len(s) > maxNodeIDLen {
		return fmt.Errorf("pki: node id must be 1 to %d characters", maxNodeIDLen)
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return fmt.Errorf("pki: node id %q has an invalid character", s)
		}
	}
	return nil
}

// IssueNode generates a fresh Ed25519 key and a certificate for node id. The
// certificate carries CN and URI SAN "unio://node/<id>", which is how peers
// learn the node's identity, plus the given hosts as DNS or IP SANs. It is
// valid for NodeCertValidity and usable for both server and client
// authentication.
func (ca *CA) IssueNode(id ids.NodeID, hosts []string) (tls.Certificate, error) {
	now := time.Now()
	return ca.issueAt(id, hosts, now.Add(-clockSkew), now.Add(NodeCertValidity))
}

// issueAt is IssueNode with explicit validity bounds, for tests.
func (ca *CA) issueAt(id ids.NodeID, hosts []string, notBefore, notAfter time.Time) (tls.Certificate, error) {
	uri, err := nodeURI(id)
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
		DNSNames:     dns,
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

// nodeIDFromCert returns the node identity carried by cert. A certificate
// must carry exactly one identity URI.
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
	return found, nil
}

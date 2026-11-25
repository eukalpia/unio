package pki

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/ids"
)

// TestNodeIDsAreDNSLabels checks the rules a node id must meet to be usable as
// a DNS label: lowercase letters, digits and hyphens, 1 to 63 characters, and
// no leading or trailing hyphen.
func TestNodeIDsAreDNSLabels(t *testing.T) {
	valid := []string{"a", "n1", "node-1", "a-b-c", "0node", strings.Repeat("a", 63)}
	invalid := []string{
		"",
		"Node1",
		"NODE",
		"node_1",
		"node.1",
		"-node",
		"node-",
		"node 1",
		"node/1",
		"nodé",
		strings.Repeat("a", 64),
	}
	for _, id := range valid {
		if err := ValidateNodeID(ids.NodeID(id)); err != nil {
			t.Errorf("valid DNS label %q rejected: %v", id, err)
		}
	}
	for _, id := range invalid {
		if err := ValidateNodeID(ids.NodeID(id)); err == nil {
			t.Errorf("invalid DNS label %q accepted", id)
		}
	}
}

// TestIssueNodeRejectsNonLabelIDs checks that the issuer refuses an id that
// would not be a DNS label, so no certificate carries an unusable name.
func TestIssueNodeRejectsNonLabelIDs(t *testing.T) {
	ca := mustCA(t, testSecret)
	for _, id := range []ids.NodeID{"Node1", "node_1", "node.1", "-node"} {
		if _, err := ca.IssueNode(id, nil); err == nil {
			t.Fatalf("IssueNode(%q) accepted an id that is not a DNS label", id)
		}
	}
}

// TestIssuedCertificateNamesNodeInDNSAndURI checks that a node certificate
// carries its identity twice: as the URI SAN, and as the DNS name
// <id>.node.unio that a dialer checks against ServerName.
func TestIssuedCertificateNamesNodeInDNSAndURI(t *testing.T) {
	ca := mustCA(t, testSecret)
	cert, err := ca.IssueNode("node-a", []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cert.Leaf.DNSNames, "node-a.node.unio") {
		t.Fatalf("DNS SANs %v lack node-a.node.unio", cert.Leaf.DNSNames)
	}
	if len(cert.Leaf.IPAddresses) != 1 {
		t.Fatalf("host IP SAN lost: IP %v", cert.Leaf.IPAddresses)
	}
	id, err := nodeIDFromCert(cert.Leaf)
	if err != nil || id != "node-a" {
		t.Fatalf("nodeIDFromCert = %q, %v; want node-a", id, err)
	}
}

// TestCertificateWithoutDNSIdentityIsRejected checks that a certificate whose
// URI names a node but whose DNS names do not name it is not accepted as that
// node's identity. The two names are issued together, so a certificate that
// disagrees with itself must be refused.
func TestCertificateWithoutDNSIdentityIsRejected(t *testing.T) {
	ca := mustCA(t, testSecret)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse("unio://node/node-x")
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: uri.String()},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{uri},
		DNSNames:     []string{"other.example"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Certificate(), pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodeIDFromCert(leaf); err == nil {
		t.Fatal("certificate whose DNS names do not name its URI identity was accepted")
	}
}

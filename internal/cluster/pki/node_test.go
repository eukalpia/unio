package pki

import (
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/ids"
)

func TestIssueNodeCertificateShape(t *testing.T) {
	ca := mustCA(t, testSecret)
	cert, err := ca.IssueNode("node1", []string{"node1.internal", "10.0.0.7", "::1"})
	if err != nil {
		t.Fatal(err)
	}
	leaf := cert.Leaf
	if leaf == nil {
		t.Fatal("IssueNode did not set Leaf")
	}
	if got, want := leaf.Subject.CommonName, "unio://node/node1"; got != want {
		t.Fatalf("CN = %q, want %q", got, want)
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != "unio://node/node1" {
		t.Fatalf("URI SANs = %v, want exactly unio://node/node1", leaf.URIs)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "node1.internal" {
		t.Fatalf("DNS SANs = %v", leaf.DNSNames)
	}
	if len(leaf.IPAddresses) != 2 || !leaf.IPAddresses[0].Equal(net.ParseIP("10.0.0.7")) || !leaf.IPAddresses[1].Equal(net.ParseIP("::1")) {
		t.Fatalf("IP SANs = %v", leaf.IPAddresses)
	}
	if leaf.IsCA {
		t.Fatal("node certificate must not be a CA")
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Fatal("node certificate lacks digital signature usage")
	}
	wantEKU := map[x509.ExtKeyUsage]bool{x509.ExtKeyUsageServerAuth: false, x509.ExtKeyUsageClientAuth: false}
	for _, eku := range leaf.ExtKeyUsage {
		if _, ok := wantEKU[eku]; ok {
			wantEKU[eku] = true
		}
	}
	for eku, seen := range wantEKU {
		if !seen {
			t.Fatalf("missing ExtKeyUsage %v", eku)
		}
	}
	if d := time.Until(leaf.NotAfter) - DefaultLifetime; d < -time.Minute || d > time.Minute {
		t.Fatalf("NotAfter is %v from now, want DefaultLifetime %v", time.Until(leaf.NotAfter), DefaultLifetime)
	}
	if got := time.Until(leaf.NotBefore); got > -4*time.Minute || got < -6*time.Minute {
		t.Fatalf("NotBefore is %v from now, want about five minutes of clock skew", got)
	}
}

func TestIssueNodeFreshKeyPerCall(t *testing.T) {
	ca := mustCA(t, testSecret)
	a, err := ca.IssueNode("node1", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ca.IssueNode("node1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.Leaf.RawSubjectPublicKeyInfo) == string(b.Leaf.RawSubjectPublicKeyInfo) {
		t.Fatal("two issuances reused the node key")
	}
	if a.Leaf.SerialNumber.Cmp(b.Leaf.SerialNumber) == 0 {
		t.Fatal("two issuances reused the serial number")
	}
}

func TestIssueNodeRejectsInvalidIDs(t *testing.T) {
	ca := mustCA(t, testSecret)
	cases := []ids.NodeID{"", "a/b", "has space", "héllo", ids.NodeID(strings.Repeat("x", 65))}
	for _, id := range cases {
		if _, err := ca.IssueNode(id, nil); err == nil {
			t.Fatalf("IssueNode(%q) accepted an invalid id", id)
		}
	}
}

func TestIssueNodeRejectsEmptyHost(t *testing.T) {
	ca := mustCA(t, testSecret)
	if _, err := ca.IssueNode("node1", []string{""}); err == nil {
		t.Fatal("empty host accepted")
	}
}

func TestNodeCertificateVerifiesAgainstOwnCA(t *testing.T) {
	ca := mustCA(t, testSecret)
	cert, err := ca.IssueNode("node1", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
		if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: ca.CertPool(), KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
			t.Fatalf("verify usage %v: %v", usage, err)
		}
	}
}

func TestForeignCARejected(t *testing.T) {
	ca := mustCA(t, testSecret)
	other := mustCA(t, []byte("ffffffffffffffffffffffffffffffff"))
	cert, err := other.IssueNode("intruder", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: ca.CertPool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		t.Fatal("certificate from a foreign CA verified")
	}
}

func TestExpiredCertificateRejected(t *testing.T) {
	ca := mustCA(t, testSecret)
	now := time.Now()
	cert, err := ca.issueAt("node1", nil, now.Add(-48*time.Hour), now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_, err = cert.Leaf.Verify(x509.VerifyOptions{Roots: ca.CertPool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	var inv x509.CertificateInvalidError
	if err == nil {
		t.Fatal("expired certificate verified")
	}
	if !errors.As(err, &inv) || inv.Reason != x509.Expired {
		t.Fatalf("error = %v, want Expired", err)
	}
}

func TestNodeIDFromCertRequiresUniqueURI(t *testing.T) {
	ca := mustCA(t, testSecret)
	cert, err := ca.IssueNode("node7", nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := nodeIDFromCert(cert.Leaf)
	if err != nil || id != "node7" {
		t.Fatalf("nodeIDFromCert = %q, %v; want node7", id, err)
	}
	cert.Leaf.URIs = nil
	if _, err := nodeIDFromCert(cert.Leaf); err == nil {
		t.Fatal("certificate without a node URI accepted")
	}
}

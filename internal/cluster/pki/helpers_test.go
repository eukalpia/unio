package pki

import (
	"crypto/tls"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

// mustCA derives a CA from secret or fails the test.
func mustCA(t *testing.T, secret []byte) *CA {
	t.Helper()
	ca, err := DeriveCA(secret)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

// mustIdentity issues a renewing identity from ca on the system clock. The
// issuer is closed when the test ends.
func mustIdentity(t *testing.T, ca *CA, id ids.NodeID) *Identity {
	t.Helper()
	ident, err := ca.NewIdentity(id, []string{"127.0.0.1"}, IssuerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ident.Close() })
	return ident
}

// mustIssuer issues a renewing issuer from ca with the given options.
func mustIssuer(t *testing.T, ca *CA, id ids.NodeID, opts IssuerOptions) *Issuer {
	t.Helper()
	is, err := ca.NewIssuer(id, []string{"127.0.0.1"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = is.Close() })
	return is
}

// currentCert returns the certificate an identity presents now.
func currentCert(t *testing.T, id *Identity) *tls.Certificate {
	t.Helper()
	cert, err := id.certificate()
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

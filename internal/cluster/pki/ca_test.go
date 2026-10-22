package pki

import (
	"bytes"
	"crypto/x509"
	"strings"
	"testing"
	"time"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func TestDeriveCARequiresLongSecret(t *testing.T) {
	cases := []struct {
		name    string
		secret  []byte
		wantErr bool
	}{
		{name: "empty", secret: nil, wantErr: true},
		{name: "31 bytes", secret: testSecret[:31], wantErr: true},
		{name: "32 bytes", secret: testSecret[:32], wantErr: false},
		{name: "long", secret: bytes.Repeat([]byte{7}, 64), wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ca, err := DeriveCA(tc.secret)
			if (err != nil) != tc.wantErr {
				t.Fatalf("DeriveCA error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && ca == nil {
				t.Fatal("nil CA without error")
			}
		})
	}
}

func TestDeriveCAIsDeterministic(t *testing.T) {
	a, err := DeriveCA(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeriveCA(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.DER(), b.DER()) {
		t.Fatal("two nodes deriving from one secret produced different CA certificates")
	}
	if !a.Certificate().Equal(b.Certificate()) {
		t.Fatal("parsed CA certificates differ")
	}
}

func TestDeriveCADiffersPerSecret(t *testing.T) {
	a, err := DeriveCA(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	other := bytes.Clone(testSecret)
	other[0] ^= 0xff
	b, err := DeriveCA(other)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.DER(), b.DER()) {
		t.Fatal("different secrets produced the same CA")
	}
}

func TestCAProperties(t *testing.T) {
	ca, err := DeriveCA(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	c := ca.Certificate()
	if !c.IsCA || !c.BasicConstraintsValid {
		t.Fatal("CA certificate lacks CA basic constraints")
	}
	if c.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatal("CA certificate cannot sign certificates")
	}
	if c.SignatureAlgorithm != x509.PureEd25519 {
		t.Fatalf("CA signature algorithm = %v, want Ed25519", c.SignatureAlgorithm)
	}
	if c.NotBefore.After(time.Now()) {
		t.Fatalf("CA not yet valid: NotBefore %v", c.NotBefore)
	}
	if c.NotAfter.Before(time.Now().AddDate(10, 0, 0)) {
		t.Fatalf("CA validity too short: NotAfter %v", c.NotAfter)
	}
	if !strings.Contains(c.Subject.CommonName, "unio") {
		t.Fatalf("unexpected CA CN %q", c.Subject.CommonName)
	}
	pool := ca.CertPool()
	if _, err := c.Verify(x509.VerifyOptions{Roots: pool}); err != nil {
		t.Fatalf("CA does not verify against its own pool: %v", err)
	}
}

package s3api_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/eukalpia/unio/internal/s3api"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// selfSignedCert writes a fresh ECDSA P-256 certificate and key for 127.0.0.1
// to a temporary directory and returns the file paths and a pool that trusts
// the certificate.
func selfSignedCert(t *testing.T) (certFile, keyFile string, roots *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "unio-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	roots.AddCert(leaf)
	return certFile, keyFile, roots
}

// TestHTTPSEndToEnd serves over TLS with a self-signed certificate and runs
// real SDK requests over https, trusting only that certificate.
func TestHTTPSEndToEnd(t *testing.T) {
	certFile, keyFile, roots := selfSignedCert(t)
	base := startServer(t, s3api.Config{TLSCertFile: certFile, TLSKeyFile: keyFile}, nil)
	if base[:8] != "https://" {
		t.Fatalf("base URL %q is not https", base)
	}
	client := newS3(base, newHTTPClient(t, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}))

	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String("secure")}); err != nil {
		t.Fatalf("CreateBucket over TLS: %v", err)
	}
	if _, err := client.PutObject(context.Background(), &s3.PutObjectInput{Bucket: aws.String("secure"), Key: aws.String("k"), Body: strings.NewReader("encrypted in transit")}); err != nil {
		t.Fatalf("PutObject over TLS: %v", err)
	}
	got, err := client.GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String("secure"), Key: aws.String("k")})
	if err != nil {
		t.Fatalf("GetObject over TLS: %v", err)
	}
	if body := readAll(t, got.Body); string(body) != "encrypted in transit" {
		t.Fatalf("body = %q", body)
	}
}

// TestTLSConfigurationErrorsFailAtNew checks that TLS misconfiguration is
// refused when the server is created, never silently served over plain HTTP.
func TestTLSConfigurationErrorsFailAtNew(t *testing.T) {
	certFile, keyFile, _ := selfSignedCert(t)
	cases := []struct {
		name string
		cfg  s3api.Config
	}{
		{"require TLS without certificate", s3api.Config{Addr: "127.0.0.1:0", RequireTLS: true}},
		{"certificate without key", s3api.Config{Addr: "127.0.0.1:0", TLSCertFile: certFile}},
		{"key without certificate", s3api.Config{Addr: "127.0.0.1:0", TLSKeyFile: keyFile}},
		{"unreadable certificate pair", s3api.Config{Addr: "127.0.0.1:0", TLSCertFile: "/nonexistent/cert.pem", TLSKeyFile: "/nonexistent/key.pem"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s3api.New(tc.cfg, fakes.NewLayer(), &fakes.Authenticator{}, &fakes.Authorizer{}, discardLogger()); err == nil {
				t.Fatal("New accepted an invalid TLS configuration")
			}
		})
	}
	if _, err := s3api.New(s3api.Config{Addr: "127.0.0.1:0", TLSCertFile: certFile, TLSKeyFile: keyFile, RequireTLS: true},
		fakes.NewLayer(), &fakes.Authenticator{}, &fakes.Authorizer{}, discardLogger()); err != nil {
		t.Fatalf("RequireTLS with a valid pair: %v", err)
	}
}

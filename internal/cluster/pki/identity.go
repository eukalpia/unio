package pki

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// Identity is a node's TLS identity: its certificate and private key, and the
// roots it trusts for peer certificates.
type Identity struct {
	Certificate tls.Certificate
	Roots       *x509.CertPool
}

// LoadIdentity loads an operator-provided identity. certFile holds the node
// certificate (PEM, leaf first), keyFile its private key (PEM) and caFile the
// PEM bundle of roots that issued peer certificates. The certificate must carry
// a unio node identity URI, and it must allow both server and client
// authentication.
func LoadIdentity(certFile, keyFile, caFile string) (*Identity, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("pki: load node key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("pki: parse node certificate: %w", err)
	}
	if _, err := nodeIDFromCert(leaf); err != nil {
		return nil, err
	}
	pair.Leaf = leaf

	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("pki: read CA file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pemBytes) {
		return nil, errors.New("pki: CA file contains no PEM certificates")
	}
	return &Identity{Certificate: pair, Roots: roots}, nil
}

// check reports whether id can build TLS configurations.
func (id *Identity) check() error {
	if id == nil || id.Roots == nil || len(id.Certificate.Certificate) == 0 || id.Certificate.PrivateKey == nil {
		return errors.New("pki: incomplete identity")
	}
	return nil
}

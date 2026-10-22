package pki

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/eukalpia/unio/internal/ids"
)

// Identity is a node's TLS identity: the certificate it presents and the roots
// it trusts for peer certificates. An identity from an Issuer presents a
// certificate that renews. An operator-provided identity presents a fixed
// certificate and is the operator's to renew.
type Identity struct {
	// Roots are the CA certificates that issued peer certificates.
	Roots *x509.CertPool

	// issuer supplies and renews the certificate. Exactly one of issuer and
	// fixed is set.
	issuer *Issuer
	// fixed is an operator-provided certificate.
	fixed *tls.Certificate
	// clock is the time source for verifying peers.
	clock Clock
}

// NewIdentity issues a renewing identity for node id from the CA. It is
// shorthand for NewIssuer followed by Identity.
func (ca *CA) NewIdentity(id ids.NodeID, hosts []string, opts IssuerOptions) (*Identity, error) {
	is, err := ca.NewIssuer(id, hosts, opts)
	if err != nil {
		return nil, err
	}
	return is.Identity(), nil
}

// LoadIdentity loads an operator-provided identity. certFile holds the node
// certificate (PEM, leaf first), keyFile its private key (PEM) and caFile the
// PEM bundle of roots that issued peer certificates. The certificate must carry
// a unio node identity URI, and it must allow both server and client
// authentication. Renewing it is the operator's job: LoadIdentity does not
// renew.
func LoadIdentity(certFile, keyFile, caFile string) (*Identity, error) {
	certFile = filepath.Clean(certFile)
	keyFile = filepath.Clean(keyFile)
	caFile = filepath.Clean(caFile)
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

	// The CA path is chosen by the operator who configures the node, and the
	// file holds public certificates only, so reading it is the purpose of
	// this function.
	pemBytes, err := os.ReadFile(caFile) // #nosec G304 -- operator-configured path, cleaned above; public CA certificates only

	if err != nil {
		return nil, fmt.Errorf("pki: read CA file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pemBytes) {
		return nil, errors.New("pki: CA file contains no PEM certificates")
	}
	return &Identity{Roots: roots, fixed: &pair, clock: SystemClock()}, nil
}

// Close releases the renewal timer of an issuer-backed identity. It does
// nothing for an operator-provided identity. Close is idempotent.
func (id *Identity) Close() error {
	if id.issuer != nil {
		return id.issuer.Close()
	}
	return nil
}

// certificate returns the certificate to present now.
func (id *Identity) certificate() (*tls.Certificate, error) {
	if id.issuer != nil {
		return id.issuer.Certificate(), nil
	}
	return id.fixed, nil
}

// now returns the time used to verify peer certificates.
func (id *Identity) now() time.Time {
	if id.clock == nil {
		return time.Now()
	}
	return id.clock.Now()
}

// check reports whether id can build TLS configurations.
func (id *Identity) check() error {
	if id == nil || id.Roots == nil || (id.issuer == nil && id.fixed == nil) {
		return errors.New("pki: incomplete identity")
	}
	return nil
}

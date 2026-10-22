package pki

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

const (
	// MinSecretLen is the minimum length of the cluster secret in bytes.
	MinSecretLen = 32

	// caInfo domain-separates the CA key from any other key derived from
	// the cluster secret, such as the metadata key-encryption key.
	caInfo = "unio cluster ca v1"
)

// CA is the cluster certificate authority. Every node derives the same CA
// from the shared cluster secret, so every node trusts every other node that
// holds the secret. A CA is safe for concurrent use.
type CA struct {
	cert *x509.Certificate
	der  []byte
	key  ed25519.PrivateKey
}

// DeriveCA deterministically derives the cluster CA from clusterSecret. The
// Ed25519 key comes from HKDF-SHA256 with the info label "unio cluster ca v1",
// and the self-signed certificate has a fixed serial and validity, so two
// nodes with the same secret produce byte-identical CA certificates.
//
// The secret must be at least MinSecretLen bytes.
func DeriveCA(clusterSecret []byte) (*CA, error) {
	if len(clusterSecret) < MinSecretLen {
		return nil, fmt.Errorf("pki: cluster secret must be at least %d bytes, got %d", MinSecretLen, len(clusterSecret))
	}
	seed, err := hkdf.Key(sha256.New, clusterSecret, nil, caInfo, ed25519.SeedSize)
	if err != nil {
		return nil, fmt.Errorf("pki: derive CA seed: %w", err)
	}
	key := ed25519.NewKeyFromSeed(seed)
	tmpl := caTemplate()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, fmt.Errorf("pki: create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("pki: parse CA certificate: %w", err)
	}
	return &CA{cert: cert, der: der, key: key}, nil
}

// caTemplate returns the fixed CA template. Serial and validity are constants
// so that every node derives the identical certificate.
func caTemplate() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unio cluster CA", Organization: []string{"Unio"}},
		NotBefore:             time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2126, time.January, 1, 0, 0, 0, 0, time.UTC),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
}

// Certificate returns the parsed CA certificate.
func (ca *CA) Certificate() *x509.Certificate { return ca.cert }

// DER returns the DER encoding of the CA certificate.
func (ca *CA) DER() []byte { return ca.der }

// PEM returns the PEM encoding of the CA certificate, for operators who
// distribute the CA to tools outside Unio.
func (ca *CA) PEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.der})
}

// CertPool returns a new pool that contains only the CA certificate.
func (ca *CA) CertPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

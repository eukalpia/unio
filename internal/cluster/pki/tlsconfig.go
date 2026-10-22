package pki

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
)

// ServerConfig returns the TLS configuration for a UniWire listener. It
// accepts TLS 1.3 only, requires a client certificate that chains to the
// identity's roots, and disables session tickets: a resumed session would
// skip the peer checks, and certificates are short-lived.
func ServerConfig(id *Identity) (*tls.Config, error) {
	if err := id.check(); err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{id.Certificate},
		ClientAuth:             tls.RequireAndVerifyClientCert,
		ClientCAs:              id.Roots,
		VerifyPeerCertificate:  verifyPeer(id.Roots, x509.ExtKeyUsageClientAuth),
		SessionTicketsDisabled: true,
	}, nil
}

// ClientConfig returns the TLS configuration for dialing a UniWire peer. It
// presents the identity's certificate and accepts only server certificates
// that chain to the identity's roots and carry a node identity.
//
// Go's standard verification checks host names, which cannot express node
// identity, so it is disabled and replaced by verifyPeer, which performs the
// chain check and the identity check. Callers must still compare the returned
// PeerID with the node they meant to dial.
func ClientConfig(id *Identity) (*tls.Config, error) {
	if err := id.check(); err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{id.Certificate},
		RootCAs:                id.Roots,
		InsecureSkipVerify:     true, // #nosec G402 -- replaced by verifyPeer below, which runs on every handshake
		VerifyPeerCertificate:  verifyPeer(id.Roots, x509.ExtKeyUsageServerAuth),
		SessionTicketsDisabled: true,
	}, nil
}

// PeerID returns the node identity of the authenticated peer of a completed
// handshake.
func PeerID(state tls.ConnectionState) (ids.NodeID, error) {
	if len(state.PeerCertificates) == 0 {
		return "", errors.New("pki: peer presented no certificate")
	}
	return nodeIDFromCert(state.PeerCertificates[0])
}

// verifyPeer returns a VerifyPeerCertificate hook that checks the leaf
// certificate against roots for usage and requires a node identity URI.
func verifyPeer(roots *x509.CertPool, usage x509.ExtKeyUsage) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return errors.New("pki: peer presented no certificate")
		}
		leaf, err := x509.ParseCertificate(raw[0])
		if err != nil {
			return fmt.Errorf("pki: parse peer certificate: %w", err)
		}
		inter := x509.NewCertPool()
		for _, der := range raw[1:] {
			c, err := x509.ParseCertificate(der)
			if err != nil {
				return fmt.Errorf("pki: parse peer intermediate: %w", err)
			}
			inter.AddCert(c)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots:         roots,
			Intermediates: inter,
			KeyUsages:     []x509.ExtKeyUsage{usage},
		}); err != nil {
			return fmt.Errorf("pki: peer certificate rejected: %w", err)
		}
		_, err = nodeIDFromCert(leaf)
		return err
	}
}

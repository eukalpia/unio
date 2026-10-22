package pki

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
)

// ServerConfig returns the TLS configuration for a UniWire listener. It
// accepts TLS 1.3 only and presents the identity's current certificate through
// GetCertificate, so a renewal reaches the next handshake without restarting
// the listener. It requests a client certificate and verifies it in
// verifyPeer, against the identity's clock. Session tickets are disabled: a
// resumed session would skip the peer checks.
//
// The built-in verification is not used because it reads the wall clock, while
// the identity's clock decides validity.
func ServerConfig(id *Identity) (*tls.Config, error) {
	if err := id.check(); err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return id.certificate()
		},
		ClientAuth:             tls.RequireAnyClientCert,
		VerifyPeerCertificate:  verifyPeer(id, x509.ExtKeyUsageClientAuth),
		SessionTicketsDisabled: true,
	}, nil
}

// ClientConfig returns the TLS configuration for dialing a UniWire peer. It
// presents the identity's current certificate through GetClientCertificate, and
// it accepts only server certificates that chain to the identity's roots, are
// valid at the identity's clock and carry a node identity.
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
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return id.certificate()
		},
		InsecureSkipVerify:     true, // #nosec G402 -- replaced by verifyPeer below, which runs on every handshake
		VerifyPeerCertificate:  verifyPeer(id, x509.ExtKeyUsageServerAuth),
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

// verifyPeer returns a VerifyPeerCertificate hook. It checks the leaf
// certificate against the identity's roots for usage, at the identity's clock,
// and requires a node identity URI.
func verifyPeer(id *Identity, usage x509.ExtKeyUsage) func([][]byte, [][]*x509.Certificate) error {
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
			Roots:         id.Roots,
			Intermediates: inter,
			KeyUsages:     []x509.ExtKeyUsage{usage},
			CurrentTime:   id.now(),
		}); err != nil {
			return fmt.Errorf("pki: peer certificate rejected: %w", err)
		}
		_, err = nodeIDFromCert(leaf)
		return err
	}
}

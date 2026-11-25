package pki

import (
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
)

// ServerConfig returns the TLS configuration for a UniWire listener. It accepts
// TLS 1.3 only and presents the identity's current certificate through
// GetCertificate, so a renewal reaches the next handshake without restarting
// the listener.
//
// It requires a client certificate that verifies against the identity's roots
// with the standard checks: chain, client-auth usage, and validity at the
// identity's clock. VerifyConnection then checks the node identity. Session
// tickets are disabled, because a resumed session would skip the peer checks.
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
		ClientAuth:             tls.RequireAndVerifyClientCert,
		ClientCAs:              id.Roots,
		Time:                   id.now,
		VerifyConnection:       verifyPeerIdentity,
		SessionTicketsDisabled: true,
	}, nil
}

// ClientConfig returns the TLS configuration for dialing a UniWire peer. It
// presents the identity's current certificate through GetClientCertificate and
// verifies the server with the standard checks: chain, server-auth usage,
// validity at the identity's clock, and the host name in ServerName.
//
// ServerName is the dialed node's DNS identity, from NodeDNSName, and the caller
// must set it on each dial. A configuration without it cannot complete a
// handshake, so the node identity is always checked. VerifyConnection then
// confirms that the URI identity of the certificate is the node that was asked
// for.
//
// Certificate verification is never disabled.
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
		RootCAs:                id.Roots,
		Time:                   id.now,
		VerifyConnection:       verifyServerIdentity,
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

// verifyPeerIdentity is the server's VerifyConnection hook. The standard checks
// have already verified the client certificate's chain, usage and validity. The
// hook requires the certificate to carry a well-formed node identity.
//
// The server's ConnectionState.ServerName is the name the client asked for, so
// it is not compared here. Only a client knows which node it meant to reach.
func verifyPeerIdentity(cs tls.ConnectionState) error {
	_, err := PeerID(cs)
	return err
}

// verifyServerIdentity is the client's VerifyConnection hook. The standard
// checks have already verified the server certificate's chain, usage, validity,
// and the host name in ServerName. The hook requires the URI identity of the
// certificate to be the node named by ServerName, so a certificate is accepted
// only for the node that was dialed.
func verifyServerIdentity(cs tls.ConnectionState) error {
	peer, err := PeerID(cs)
	if err != nil {
		return err
	}
	want, err := NodeDNSName(peer)
	if err != nil {
		return err
	}
	if cs.ServerName != want {
		return fmt.Errorf("pki: server certificate is for node %s, but node %s was dialed", peer, cs.ServerName)
	}
	return nil
}

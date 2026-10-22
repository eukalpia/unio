// Package pki bootstraps internode trust for UniWire.
//
// Every node derives the same cluster CA from UNIO_CLUSTER_SECRET (HKDF-SHA256
// to an Ed25519 key, then a deterministic self-signed certificate). The node
// then issues its own certificate from that CA. A node identity is the URI SAN
// unio://node/<id>. Peers accept only certificates that chain to the CA and
// are valid at their clock.
//
// Node certificates are short-lived. An Issuer holds the CA and the node's
// identity, and it renews the certificate when half of its lifetime has
// elapsed, with a fresh key each time. The default lifetime is seven days and
// the minimum is one hour. NotBefore is backdated by five minutes for clock
// skew. Renewal is driven by a timer on an injectable Clock. Handshakes read
// the current certificate with an atomic load, through the GetCertificate and
// GetClientCertificate hooks of the TLS configurations, so renewals reach new
// handshakes without restarts. Existing connections keep the certificate they
// negotiated. Close the Identity or Issuer when the node stops.
//
// Operators may supply fixed certificates instead, with LoadIdentity. Those are
// not renewed.
//
// ServerConfig and ClientConfig return TLS 1.3 configurations for mutual TLS.
// PeerID extracts the authenticated node id of a completed handshake.
package pki

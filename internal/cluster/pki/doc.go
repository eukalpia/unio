// Package pki bootstraps internode trust for UniWire.
//
// Every node derives the same cluster CA from UNIO_CLUSTER_SECRET (HKDF-SHA256
// to an Ed25519 key, then a deterministic self-signed certificate), issues
// itself a certificate from it, and accepts only peers whose certificates chain
// to that CA. A node identity is the URI SAN unio://node/<id>. Operators who
// prefer their own certificates load them with LoadIdentity instead.
//
// ServerConfig and ClientConfig return TLS 1.3 configurations for mutual TLS.
// PeerID extracts the authenticated node id of a completed handshake.
package pki

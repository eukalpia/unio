// Package pki bootstraps internode trust for UniWire.
//
// Every node derives the same cluster CA from UNIO_CLUSTER_SECRET (HKDF-SHA256
// to an Ed25519 key, then a deterministic self-signed certificate). The node
// then issues its own certificate from that CA. A node id is a DNS label, and
// its certificate names the node twice: as the URI SAN unio://node/<id>, and as
// the DNS name <id>.node.unio. A dialer sets ServerName to the DNS name of the
// node it wants, so the standard checks bind the server certificate to that
// node. Peers accept only certificates that chain to the CA, carry the key
// usage for their role, and are valid at their clock.
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
// Certificate verification is never disabled. ServerConfig requires and
// verifies client certificates, and ClientConfig verifies servers against
// ServerName. Both check the node identity in VerifyConnection.
//
// Operators may supply fixed certificates instead, with LoadIdentity. Those are
// not renewed, and they must carry the same URI and DNS identity.
package pki

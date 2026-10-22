// Package clustertest provides test doubles for the cluster layer: an in-memory
// drive that follows the drive contract, and a loopback cluster of UniWire
// transports that trust one CA, so tests exercise real mutual TLS. The package
// is for tests only.
package clustertest

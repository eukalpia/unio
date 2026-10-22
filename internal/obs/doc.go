// Package obs provides the observability primitives shared by the Unio
// server and CLI: structured logging with secret redaction, request ids,
// Prometheus metrics, bearer-token protection for debug endpoints, and
// liveness and readiness handlers (architecture sections 10 and 11).
//
// Nothing in this package uses global state. Metrics live in a registry
// owned by each Metrics value, and every handler is built by a constructor
// that returns an error for invalid configuration, such as an empty token.
package obs

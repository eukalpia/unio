// Package security is a black-box attack suite for S3 endpoints.
//
// It sends forged, tampered, malformed and abusive requests to a running
// server and asserts that each one is rejected without side effects and
// without leaking internal details. It knows nothing about Unio's
// internals, so it can be validated against another S3 implementation
// before it is used to judge Unio.
//
// The suite is skipped unless UNIO_TEST_ENDPOINT is set. See README.md.
package security

// Package sigv4 verifies AWS Signature Version 4 requests for the S3 API.
//
// It supports the header form (Authorization header), the presigned form
// (X-Amz-* query parameters) and the payload variants: UNSIGNED-PAYLOAD,
// a hex SHA-256 digest, STREAMING-AWS4-HMAC-SHA256-PAYLOAD (per-chunk
// signatures), STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER (signed trailer)
// and STREAMING-UNSIGNED-PAYLOAD-TRAILER (unsigned chunks, trailing
// checksum).
//
// Secrets never leave this package: they are used to derive signing keys
// and are not logged or included in errors. Signatures are compared in
// constant time.
package sigv4

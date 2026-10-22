// Package sigv4 verifies AWS Signature Version 4 requests for the S3 API.
//
// It supports the header form (Authorization header), the presigned form
// (X-Amz-* query parameters) and the payload variants: UNSIGNED-PAYLOAD,
// a hex SHA-256 digest, STREAMING-AWS4-HMAC-SHA256-PAYLOAD (per-chunk
// signatures), STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER (signed trailer)
// and STREAMING-UNSIGNED-PAYLOAD-TRAILER (unsigned chunks, trailing
// checksum).
//
// # Signed headers
//
// A header is only protected if the signature covers it, so the signed
// header list is checked against the request:
//
//   - host is always signed, and so is date when it is the signing time;
//   - x-amz-content-sha256 must be present in a header request. It is the
//     only binding of the body to the signature, so it must be signed too;
//   - every x-amz-* header present must be signed, as must Content-MD5 and
//     the object headers S3 stores (Content-Type, Cache-Control,
//     Content-Disposition, Content-Encoding, Content-Language, Expires).
//
// A missing signature on a present header fails with AccessDenied and the
// message "There were headers present in the request which were not
// signed". The AWS SDKs sign every header they send, except Authorization,
// User-Agent, X-Amzn-Trace-Id, Expect and Transfer-Encoding. Other headers,
// such as Accept-Encoding, are not covered by this rule.
//
// Secrets never leave this package: they are used to derive signing keys
// and are not logged or included in errors. Signatures are compared in
// constant time.
package sigv4

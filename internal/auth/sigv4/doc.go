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
// A header is protected only if the signature covers it. What must be signed
// depends on the form of the request.
//
// Header auth (the Authorization header) requires:
//
//   - host, and date when it is the signing time;
//   - x-amz-content-sha256, present and signed. It is the only binding of
//     the body to the signature. An unsigned digest could be replaced
//     together with the body;
//   - every x-amz-* header that is present;
//   - Content-MD5, Content-Type, Cache-Control, Content-Disposition,
//     Content-Encoding, Content-Language and Expires, when present. An
//     unsigned Content-MD5 could be replaced together with the body, and the
//     others are stored with the object.
//
// A presigned URL (X-Amz-* query parameters) requires:
//
//   - host;
//   - every x-amz-* header that is present.
//
// The object headers listed above may be unsigned on a presigned URL. The
// URL is generated before the request is sent, and a client such as a
// browser adds Content-Type after presigning. S3 accepts that flow, and so
// does this server. When one of those headers is listed in X-Amz-SignedHeaders,
// its value must match, as for any signed header. A presigned request's body
// is covered by UNSIGNED-PAYLOAD and so is not signed. A client that wants
// the body bound lists the checksum or Content-MD5 in X-Amz-SignedHeaders.
// x-amz-checksum-* headers are always signed, so they bind the body in both
// forms.
//
// A missing signature on a required header fails with AccessDenied and the
// message "There were headers present in the request which were not
// signed". The AWS SDKs sign every header they send, except Authorization,
// User-Agent, X-Amzn-Trace-Id, Expect and Transfer-Encoding, so SDK requests
// meet this rule in both forms. Headers outside the rule, such as
// Accept-Encoding, are never covered and do not affect the signature.
//
// Secrets never leave this package: they are used to derive signing keys
// and are not logged or included in errors. Signatures are compared in
// constant time.
package sigv4

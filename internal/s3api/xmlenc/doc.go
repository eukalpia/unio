// Package xmlenc is a hand-written, allocation-light XML encoder for S3
// response bodies.
//
// Every Append function appends to a caller-supplied buffer and returns the
// extended slice, so a reused buffer serves repeated responses without
// allocating. Output starts with an XML declaration, has no trailing newline,
// and carries the S3 namespace on success roots. The Error root has no
// namespace.
//
// Text is escaped by AppendEscape. XML 1.0 cannot represent C0 controls other
// than tab, LF and CR, U+FFFE, U+FFFF or surrogates, and invalid UTF-8 has no
// XML representation at all. Each such byte or code point is replaced with
// U+FFFD. Callers that need the exact bytes of a key must request
// encoding-type=url, which percent-encodes key-like values with AppendPercent.
package xmlenc

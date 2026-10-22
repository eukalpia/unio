// Package headers parses the S3 request headers that shape object
// operations: byte ranges, preconditions, additional checksums, user
// metadata, tagging, copy sources and destination attributes.
//
// Every input is untrusted. Parsers enforce explicit limits on lengths and
// counts, reject ambiguous input instead of guessing, and return typed
// results from the object package. Client errors are *s3err.Error values
// with client-safe messages; internal causes are never included.
package headers

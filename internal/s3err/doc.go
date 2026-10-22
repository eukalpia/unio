// Package s3err defines S3 error codes, their HTTP status codes and the
// typed errors that carry them.
//
// Client-visible information is limited to the code and a client-safe
// message. Internal causes are wrapped for logging and never sent to
// clients.
//
// Domain packages declare their sentinel errors with NewSentinel so that
// every error can be mapped to an S3 code (CodeOf) without this package
// knowing about the domains.
package s3err

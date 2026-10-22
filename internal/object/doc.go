// Package object defines the object storage API that the S3 front end
// calls: the Layer interface, object and bucket descriptions, request
// options, verified payloads (Body), protocol limits and input validation.
//
// The implementation lives in package object/engine; package
// object/objecttest provides an in-memory fake and the conformance suite
// that every Layer implementation must pass.
package object

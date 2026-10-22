// Package core defines the contracts shared by every Unio module: identifiers,
// typed errors, metadata records and the interfaces between layers.
//
// The dependency direction is strict. Implementation packages (s3api, auth,
// erasure, storage, meta, cluster, object) import core and never each other.
// They are wired together only by the node assembly in internal/server.
//
// Every interface in this package has an in-memory fake and a conformance
// suite in package coretest. A real implementation is correct only if it
// passes the same suite as the fake.
//
// Changing anything in this package is a contract change and requires an ADR.
package core

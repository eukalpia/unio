// Package fakes provides in-memory test doubles for the S3 front end: an
// object.Layer, an auth.Authenticator that does not verify signatures and
// an auth.Authorizer that records its calls. They exist for tests only and
// must not be used in production code.
package fakes

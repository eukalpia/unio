// Package authtest provides fakes for tests of packages that depend on
// authentication and authorization: an in-memory meta.SystemKV with the
// revision rules of the real store, a static authenticator that accepts one
// access key pair, and allow-all and deny-all authorizers.
//
// The fakes are for tests only. They are not safe to expose to clients.
package authtest

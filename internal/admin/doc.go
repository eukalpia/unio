// Package admin serves the administration API under /unio/admin/v1/. The
// console and the command-line tool manage buckets, objects, users, access
// keys and the cluster through it.
//
// A request is authenticated in one of two ways. A SigV4 signature from a
// principal allowed unio:Admin is verified by the IAM store. A console session
// cookie, unio_session, is an HMAC-authenticated token issued by POST session;
// it is checked again against IAM on every request, so a disabled user or a
// revoked right stops working within the IAM cache time. Requests that use the
// cookie and change state must also carry the X-Unio-CSRF token that the
// login returned.
//
// The package has no transport dependency. Service.Serve takes a Request and
// returns a Response, and the S3 front end maps between those and its HTTP
// context. Every failure is an error response with an S3-style code and a
// client-safe message; internal causes are logged with the request id only.
//
// The package keeps no goroutines and no timers. The login limiter and the
// request statistics have fixed memory bounds.
package admin

package admin

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/auth/iam"
	"github.com/eukalpia/unio/internal/s3err"
)

// TestClientMessages checks which message reaches the client for an error.
func TestClientMessages(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "client text of the same code is sent",
			err:  s3err.Errorf(s3err.InvalidBucketName, "bucket name must be 3-63 characters long"),
			want: "bucket name must be 3-63 characters long",
		},
		{
			name: "a package-prefixed message is replaced",
			err:  iam.ErrUserExists,
			want: "The entity already exists.",
		},
		{
			name: "a wrapped package message is replaced",
			err:  fmt.Errorf("admin: create: %w", iam.ErrInvalidUserName),
			want: "A parameter is not valid.",
		},
		{
			name: "a plain error is an internal error with fixed text",
			err:  errors.New("disk /var/lib/unio/x is gone"),
			want: "We encountered an internal error. Retry the request.",
		},
		{
			name: "a prefixed message of a code without fixed text has the generic sentence",
			err:  s3err.Errorf(s3err.MalformedXML, "xml: unexpected end"),
			want: "The request could not be completed.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, code, _ := describe(tc.err)
			if got := clientMessage(tc.err, code); got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDescribe checks the status and code of the error classes the API uses.
func TestDescribe(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   s3err.Code
	}{
		{"denied", errDenied(), http.StatusForbidden, s3err.AccessDenied},
		{"unauthenticated", errUnauthenticated(), http.StatusUnauthorized, s3err.AccessDenied},
		{"bad request", errBadRequest("x"), http.StatusBadRequest, s3err.InvalidRequest},
		{"invalid", errInvalid("x"), http.StatusBadRequest, s3err.InvalidArgument},
		{"not found", errNotFound(), http.StatusNotFound, s3err.NoSuchEntity},
		{"method", errMethodNotAllowed(), http.StatusMethodNotAllowed, s3err.MethodNotAllowed},
		{"too large", errTooLarge(), http.StatusRequestEntityTooLarge, s3err.EntityTooLarge},
		{"unsupported media", errUnsupportedMedia(), http.StatusUnsupportedMediaType, s3err.InvalidRequest},
		{"root", errRootProtected(), http.StatusConflict, RootUserProtected},
		{"login limited", errLoginLimited(), http.StatusTooManyRequests, s3err.SlowDown},
		{"not implemented", errNamedPoliciesUnsupported(), http.StatusNotImplemented, s3err.NotImplemented},
		{"bucket not empty", s3err.Errorf(s3err.BucketNotEmpty, "x"), http.StatusConflict, s3err.BucketNotEmpty},
		{"internal", errors.New("boom"), http.StatusInternalServerError, s3err.InternalError},
		{"wrapped denial", fmt.Errorf("ctx: %w", errDenied()), http.StatusForbidden, s3err.AccessDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code, _ := describe(tc.err)
			if status != tc.status || code != tc.code {
				t.Errorf("describe = %d %s, want %d %s", status, code, tc.status, tc.code)
			}
		})
	}
}

// TestAuthzErrorKeepsStoreFailures checks that a store failure is not reported
// as a denial, and that a refusal is.
func TestAuthzErrorKeepsStoreFailures(t *testing.T) {
	if got := authzError(s3err.Errorf(s3err.AccessDenied, "no")); !isStatus(got, http.StatusForbidden) {
		t.Errorf("a refusal became %v", got)
	}
	if got := authzError(errors.New("store down")); !isStatus(got, http.StatusInternalServerError) {
		t.Errorf("a store failure became %v", got)
	}
	if got := authzError(s3err.Errorf(s3err.ServiceUnavailable, "retry")); !isStatus(got, http.StatusServiceUnavailable) {
		t.Errorf("an unavailable store became %v", got)
	}
}

// isStatus reports whether err describes the status.
func isStatus(err error, status int) bool {
	got, _, _ := describe(err)
	return got == status
}

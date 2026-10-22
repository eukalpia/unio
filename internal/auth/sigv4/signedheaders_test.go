package sigv4

import (
	"context"
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/auth"

	"github.com/eukalpia/unio/internal/s3err"
)

// signedGet returns a GET request signed by the SDK. The headers in before
// are present when the signature is computed; the headers in after are added
// to the request later, as an intermediary would. The S3 payload hash is
// always sent and signed.
func signedGet(t testing.TB, before, after http.Header) *auth.Request {
	t.Helper()
	r := newHTTPRequest(t, http.MethodGet, "https://bucket.s3.example.com/key", nil)
	for k, vs := range before {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	r.Header.Set("X-Amz-Content-Sha256", emptyPayloadSHA256)
	signer := newSDKSigner()
	if err := signer.SignHTTP(context.Background(), sdkCredentials(), r, emptyPayloadSHA256, "s3", testRegion, docNow); err != nil {
		t.Fatal(err)
	}
	for k, vs := range after {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	return authRequest(t, r)
}

// TestUnsignedHeadersAreRejected checks that a header S3 requires to be signed
// fails the request when it is present but not covered by the signature. The
// signature itself is valid: the intermediary only added the header.
func TestUnsignedHeadersAreRejected(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"user metadata", "X-Amz-Meta-Owner", "mallory"},
		{"copy source", "X-Amz-Copy-Source", "other-bucket/secret"},
		{"checksum", "X-Amz-Checksum-Crc32c", "AAAAAA=="},
		{"tagging", "X-Amz-Tagging", "owner=mallory"},
		{"canned acl", "X-Amz-Acl", "public-read"},
		{"storage class", "X-Amz-Storage-Class", "STANDARD"},
		{"content-md5", "Content-Md5", "1B2M2Y8AsgTpgAmY7PhCfg=="},
		{"content-type", "Content-Type", "text/html"},
		{"cache-control", "Cache-Control", "public, max-age=31536000"},
		{"content-disposition", "Content-Disposition", "attachment"},
		{"content-encoding", "Content-Encoding", "gzip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			after := http.Header{}
			after.Set(c.key, c.value)
			req := signedGet(t, nil, after)
			a := newTestAuthenticator(docNow)
			_, err := a.Authenticate(context.Background(), req)
			if !s3err.Is(err, s3err.AccessDenied) {
				t.Fatalf("err = %v, want AccessDenied for an unsigned %s header", err, c.key)
			}
		})
	}
}

// TestSignedHeadersAreAccepted checks the same headers when the client signs
// them, as the SDK does, so that correct clients keep working.
func TestSignedHeadersAreAccepted(t *testing.T) {
	before := http.Header{}
	before.Set("X-Amz-Meta-Owner", "alice")
	before.Set("X-Amz-Storage-Class", "STANDARD")
	before.Set("Content-Type", "text/plain")
	before.Set("Cache-Control", "no-store")
	req := signedGet(t, before, nil)
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), req); err != nil {
		t.Fatalf("signed headers rejected: %v", err)
	}
}

// TestUnsignedHeaderOutsideTheRuleIsAccepted checks that headers the rule does
// not cover do not break the signature, as before.
func TestUnsignedHeaderOutsideTheRuleIsAccepted(t *testing.T) {
	after := http.Header{}
	after.Set("X-Client-Trace", "anything")
	after.Set("Accept-Encoding", "gzip")
	req := signedGet(t, nil, after)
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), req); err != nil {
		t.Fatalf("header outside the rule rejected: %v", err)
	}
}

// TestPresignedRequestWithUnsignedAmzHeaderIsRejected checks the rule on the
// presigned path, where only host is signed.
func TestPresignedRequestWithUnsignedAmzHeaderIsRejected(t *testing.T) {
	signed := presignURL(t, newHTTPRequest(t, http.MethodGet, "https://bucket.s3.example.com/key", nil), 3600, docNow)
	r := newHTTPRequest(t, http.MethodGet, signed, nil)
	r.Header.Set("X-Amz-Meta-Owner", "mallory")
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("err = %v, want AccessDenied", err)
	}
}

// TestUnsignedPayloadHashHeaderIsRejected models the attack the rule exists
// for: the body digest is added after signing. The digest is then not bound
// by the signature, so an intermediary could replace the body together with
// its digest.
func TestUnsignedPayloadHashHeaderIsRejected(t *testing.T) {
	r := newHTTPRequest(t, http.MethodGet, "https://bucket.s3.example.com/key", nil)
	signer := newSDKSigner()
	if err := signer.SignHTTP(context.Background(), sdkCredentials(), r, emptyPayloadSHA256, "s3", testRegion, docNow); err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Amz-Content-Sha256", emptyPayloadSHA256)
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("err = %v, want AccessDenied for an unsigned x-amz-content-sha256", err)
	}
}

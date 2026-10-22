package sigv4

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/eukalpia/unio/internal/auth"
)

// The example credentials from the AWS documentation. They are public
// test values, not secrets.
const (
	docAccessKey = "AKIAIOSFODNN7EXAMPLE"
	docSecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	testRegion   = "us-east-1"
)

// fakeStore is a CredentialStore backed by a map.
type fakeStore struct {
	keys map[string]fakeKey
}

type fakeKey struct {
	secret string
	id     auth.Identity
}

// Lookup implements CredentialStore.
func (s fakeStore) Lookup(_ context.Context, ak string) (string, auth.Identity, error) {
	k, ok := s.keys[ak]
	if !ok {
		return "", auth.Identity{}, ErrUnknownAccessKey
	}
	return k.secret, k.id, nil
}

// newTestStore returns a store that knows the documentation key pair.
func newTestStore() fakeStore {
	return fakeStore{keys: map[string]fakeKey{
		docAccessKey: {secret: docSecretKey, id: auth.Identity{User: "alice"}},
	}}
}

// fixedClock returns a clock that always reads t.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// authRequest converts a raw HTTP request into the transport-independent
// view that the authenticator takes. The body, if any, is wrapped as an
// io.Reader.
func authRequest(t *testing.T, r *http.Request) *auth.Request {
	t.Helper()
	var body io.Reader
	if r.Body != nil {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	return &auth.Request{
		Method:        r.Method,
		Host:          r.Host,
		RawPath:       r.URL.EscapedPath(),
		RawQuery:      r.URL.RawQuery,
		Header:        r.Header.Clone(),
		Body:          body,
		ContentLength: r.ContentLength,
	}
}

// newHTTPRequest builds a request for the given absolute URL.
func newHTTPRequest(t *testing.T, method, rawURL string, body []byte) *http.Request {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	r, err := http.NewRequest(method, rawURL, rd)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = u.Host
	r.ContentLength = int64(len(body))
	if body == nil {
		r.ContentLength = 0
	}
	return r
}

// sdkCredentials wraps the documentation key pair for the SDK signer.
func sdkCredentials() aws.Credentials {
	return aws.Credentials{AccessKeyID: docAccessKey, SecretAccessKey: docSecretKey}
}

// signHeader signs r with the SDK signer for S3 (path not re-escaped, as
// S3 requires) and returns it.
func signHeader(t *testing.T, r *http.Request, payloadHash string, when time.Time) *http.Request {
	t.Helper()
	signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	r.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if err := signer.SignHTTP(context.Background(), sdkCredentials(), r, payloadHash, "s3", testRegion, when); err != nil {
		t.Fatal(err)
	}
	return r
}

// presignURL returns a presigned URL for r produced by the SDK presigner,
// valid for expires seconds from when.
func presignURL(t *testing.T, r *http.Request, expires int, when time.Time) string {
	t.Helper()
	q := r.URL.Query()
	q.Set("X-Amz-Expires", strconv.Itoa(expires))
	r.URL.RawQuery = q.Encode()
	signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	signed, _, err := signer.PresignHTTP(context.Background(), sdkCredentials(), r, unsignedPayload, "s3", testRegion, when)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

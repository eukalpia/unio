package sigv4

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/eukalpia/unio/internal/s3err"
)

// presignedPutURL returns a presigned PUT URL for the upload key. The URL is
// signed before any content header is added, as a browser upload flow does.
func presignedPutURL(t testing.TB) string {
	t.Helper()
	return presignURL(t, newHTTPRequest(t, http.MethodPut, "https://bucket.s3.example.com/upload.png", nil), 3600, docNow)
}

// TestPresignedPutWithUnsignedContentHeadersIsAccepted checks that headers the
// client adds after presigning are accepted. The standard browser upload flow
// presigns a PUT without Content-Type and the browser adds it. S3 accepts this,
// so the object headers are not required to be signed on presigned URLs.
func TestPresignedPutWithUnsignedContentHeadersIsAccepted(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"content-type", "Content-Type", "image/png"},
		{"content-md5", "Content-Md5", "1B2M2Y8AsgTpgAmY7PhCfg=="},
		{"cache-control", "Cache-Control", "no-store"},
		{"content-disposition", "Content-Disposition", "inline"},
		{"content-encoding", "Content-Encoding", "gzip"},
		{"content-language", "Content-Language", "en"},
		{"expires", "Expires", "Wed, 21 Oct 2026 07:28:00 GMT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newHTTPRequest(t, http.MethodPut, presignedPutURL(t), nil)
			r.Header.Set(c.key, c.value)
			a := newTestAuthenticator(docNow)
			if _, err := a.Authenticate(context.Background(), authRequest(t, r)); err != nil {
				t.Fatalf("presigned PUT with an unsigned %s rejected: %v", c.key, err)
			}
		})
	}
}

// TestPresignedPutWithUnsignedAmzHeaderIsRejected checks that the x-amz-*
// rule still applies to presigned PUT requests, whatever the method.
func TestPresignedPutWithUnsignedAmzHeaderIsRejected(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"user metadata", "X-Amz-Meta-Owner", "mallory"},
		{"checksum", "X-Amz-Checksum-Crc32c", "AAAAAA=="},
		{"tagging", "X-Amz-Tagging", "owner=mallory"},
		{"canned acl", "X-Amz-Acl", "public-read"},
		{"copy source", "X-Amz-Copy-Source", "other-bucket/secret"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newHTTPRequest(t, http.MethodPut, presignedPutURL(t), nil)
			r.Header.Set(c.key, c.value)
			a := newTestAuthenticator(docNow)
			_, err := a.Authenticate(context.Background(), authRequest(t, r))
			if !s3err.Is(err, s3err.AccessDenied) {
				t.Fatalf("err = %v, want AccessDenied for an unsigned %s", err, c.key)
			}
		})
	}
}

// TestHeaderAuthUnsignedContentTypeIsRejected checks the header-auth rule for
// the object headers: a Content-Type the signature does not cover fails the
// request. The presigned relaxation must not leak into header auth.
func TestHeaderAuthUnsignedContentTypeIsRejected(t *testing.T) {
	after := http.Header{}
	after.Set("Content-Type", "text/html")
	req := signedGet(t, nil, after)
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), req); !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("err = %v, want AccessDenied for an unsigned Content-Type on header auth", err)
	}
}

// TestPresignedSignedContentTypeMustMatch checks a Content-Type that the client
// did list in the signature. The request must send the signed value. A
// different value fails with SignatureDoesNotMatch, as before.
func TestPresignedSignedContentTypeMustMatch(t *testing.T) {
	r := newHTTPRequest(t, http.MethodPut, "https://bucket.s3.example.com/upload.png", nil)
	r.Header.Set("Content-Type", "image/png")
	q := r.URL.Query()
	q.Set("X-Amz-Expires", "3600")
	r.URL.RawQuery = q.Encode()
	signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	signedURI, signedHeaders, err := signer.PresignHTTP(context.Background(), aws.Credentials{
		AccessKeyID: docAccessKey, SecretAccessKey: docSecretKey,
	}, r, unsignedPayload, "s3", testRegion, docNow)
	if err != nil {
		t.Fatal(err)
	}
	a := newTestAuthenticator(docNow)

	ok := newHTTPRequest(t, http.MethodPut, signedURI, nil)
	for k, vs := range signedHeaders {
		for _, v := range vs {
			ok.Header.Add(k, v)
		}
	}
	if _, err := a.Authenticate(context.Background(), authRequest(t, ok)); err != nil {
		t.Fatalf("presigned PUT with the signed Content-Type rejected: %v", err)
	}

	bad := newHTTPRequest(t, http.MethodPut, signedURI, nil)
	bad.Header.Set("Content-Type", "text/html")
	if _, err := a.Authenticate(context.Background(), authRequest(t, bad)); !s3err.Is(err, s3err.SignatureDoesNotMatch) {
		t.Fatalf("err = %v, want SignatureDoesNotMatch for a changed signed Content-Type", err)
	}
}

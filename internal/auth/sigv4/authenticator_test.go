package sigv4

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

// docNow is the request time of the AWS documentation examples.
var docNow = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

// awsGetRequest is the GET Object example from the S3 SigV4 documentation.
// The signature is the one published there.
func awsGetRequest(t *testing.T) *http.Request {
	t.Helper()
	r := newHTTPRequest(t, http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	r.Header.Set("Range", "bytes=0-9")
	r.Header.Set("X-Amz-Content-Sha256", emptyPayloadSHA256)
	r.Header.Set("X-Amz-Date", "20130524T000000Z")
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+docAccessKey+"/20130524/us-east-1/s3/aws4_request,"+
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,"+
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41")
	return r
}

func newTestAuthenticator(now time.Time) *Authenticator {
	return New(newTestStore(), Options{Region: testRegion, Clock: fixedClock(now)})
}

func TestAuthenticateAWSDocumentedGetVector(t *testing.T) {
	a := newTestAuthenticator(docNow)
	res, err := a.Authenticate(context.Background(), authRequest(t, awsGetRequest(t)))
	if err != nil {
		t.Fatalf("documented example rejected: %v", err)
	}
	if res.Identity.AccessKey != docAccessKey || res.Identity.User != "alice" {
		t.Fatalf("identity = %+v", res.Identity)
	}
	if !res.SignedTime.Equal(docNow) {
		t.Fatalf("SignedTime = %v, want %v", res.SignedTime, docNow)
	}
	if !bytes.Equal(res.PayloadSHA256, emptyDigest[:]) {
		t.Fatalf("PayloadSHA256 = %x, want the empty digest", res.PayloadSHA256)
	}
}

func TestAuthenticateAWSDocumentedPresignVector(t *testing.T) {
	const raw = "https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host" +
		"&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	r := newHTTPRequest(t, http.MethodGet, raw, nil)
	a := newTestAuthenticator(docNow.Add(time.Hour))
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); err != nil {
		t.Fatalf("documented presigned example rejected: %v", err)
	}
}

func TestAuthenticateAcceptsSDKSignedRequests(t *testing.T) {
	cases := []struct {
		name   string
		method string
		url    string
		body   []byte
	}{
		{"root", http.MethodGet, "https://bucket.s3.example.com/", nil},
		{"plain key", http.MethodGet, "https://bucket.s3.example.com/a/b.txt", nil},
		{"key with spaces and symbols", http.MethodPut, "https://bucket.s3.example.com/dir/a%20b%24c%2Bd.txt", []byte("payload")},
		{"unicode key", http.MethodGet, "https://bucket.s3.example.com/%E2%82%AC/%C3%A9.bin", nil},
		{"tilde and dash", http.MethodGet, "https://bucket.s3.example.com/~x-y_z.v1", nil},
		{"query with repeated keys", http.MethodGet, "https://bucket.s3.example.com/?list-type=2&prefix=a%2Fb&prefix=c", nil},
		{"query with empty value", http.MethodGet, "https://bucket.s3.example.com/?uploads=", nil},
		{"port in host", http.MethodHead, "http://127.0.0.1:9000/bucket/key", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newHTTPRequest(t, c.method, c.url, c.body)
			hash := emptyPayloadSHA256
			if c.body != nil {
				sum := sha256.Sum256(c.body)
				hash = hex.EncodeToString(sum[:])
			}
			signed := signHeader(t, r, hash, docNow)
			a := newTestAuthenticator(docNow)
			res, err := a.Authenticate(context.Background(), authRequest(t, signed))
			if err != nil {
				t.Fatalf("SDK-signed request rejected: %v", err)
			}
			if res.Identity.AccessKey != docAccessKey {
				t.Fatalf("identity = %+v", res.Identity)
			}
		})
	}
}

func TestAuthenticateAcceptsSDKPresignedURLs(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"object", "https://bucket.s3.example.com/key.txt"},
		{"escaped key", "https://bucket.s3.example.com/a%20b%2Bc/%E2%82%AC.txt"},
		{"with extra query", "https://bucket.s3.example.com/key?response-content-type=text%2Fplain"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newHTTPRequest(t, http.MethodGet, c.url, nil)
			signed := presignURL(t, r, 3600, docNow)
			pr := newHTTPRequest(t, http.MethodGet, signed, nil)
			a := newTestAuthenticator(docNow.Add(10 * time.Minute))
			if _, err := a.Authenticate(context.Background(), authRequest(t, pr)); err != nil {
				t.Fatalf("SDK-presigned URL rejected: %v", err)
			}
		})
	}
}

func TestAuthenticateMutationsAreRejected(t *testing.T) {
	type mutation struct {
		name   string
		mutate func(req *auth.Request)
		want   s3err.Code
	}
	tamper := []mutation{
		{"path changed", func(r *auth.Request) { r.RawPath = "/other.txt" }, s3err.SignatureDoesNotMatch},
		{"query added", func(r *auth.Request) { r.RawQuery = "x=1" }, s3err.SignatureDoesNotMatch},
		{"method changed", func(r *auth.Request) { r.Method = http.MethodPut }, s3err.SignatureDoesNotMatch},
		{"host changed", func(r *auth.Request) { r.Host = "evil.example.com" }, s3err.SignatureDoesNotMatch},
		{"signed header changed", func(r *auth.Request) { r.Header.Set("Range", "bytes=0-99") }, s3err.SignatureDoesNotMatch},
		{"signed date changed", func(r *auth.Request) { r.Header.Set("X-Amz-Date", "20130524T000100Z") }, s3err.SignatureDoesNotMatch},
		{"payload hash changed", func(r *auth.Request) {
			r.Header.Set("X-Amz-Content-Sha256", strings.Repeat("0", 64))
		}, s3err.SignatureDoesNotMatch},
		{"signature byte flipped", func(r *auth.Request) {
			r.Header.Set("Authorization", strings.Replace(r.Header.Get("Authorization"),
				"f0e8bdb8", "f0e8bdb9", 1))
		}, s3err.SignatureDoesNotMatch},
		{"unknown access key", func(r *auth.Request) {
			r.Header.Set("Authorization", strings.Replace(r.Header.Get("Authorization"),
				docAccessKey, "AKIAUNKNOWN0000000", 1))
		}, s3err.InvalidAccessKeyID},
	}
	for _, m := range tamper {
		t.Run(m.name, func(t *testing.T) {
			req := authRequest(t, awsGetRequest(t))
			m.mutate(req)
			a := newTestAuthenticator(docNow)
			_, err := a.Authenticate(context.Background(), req)
			if !s3err.Is(err, m.want) {
				t.Fatalf("err = %v, want code %s", err, m.want)
			}
		})
	}
}

func TestAuthenticateUnsignedHeaderIsAccepted(t *testing.T) {
	r := awsGetRequest(t)
	r.Header.Set("X-Client-Trace", "anything")
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); err != nil {
		t.Fatalf("an unsigned header must not break the signature: %v", err)
	}
}

func TestAuthenticateClockSkew(t *testing.T) {
	cases := []struct {
		name  string
		now   time.Time
		want  s3err.Code
		isErr bool
	}{
		{"exact", docNow, "", false},
		{"14 minutes ahead", docNow.Add(14 * time.Minute), "", false},
		{"16 minutes ahead", docNow.Add(16 * time.Minute), s3err.RequestTimeTooSkewed, true},
		{"16 minutes behind", docNow.Add(-16 * time.Minute), s3err.RequestTimeTooSkewed, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newTestAuthenticator(c.now)
			_, err := a.Authenticate(context.Background(), authRequest(t, awsGetRequest(t)))
			if c.isErr != (err != nil) {
				t.Fatalf("err = %v", err)
			}
			if c.isErr && !s3err.Is(err, c.want) {
				t.Fatalf("code = %s, want %s", s3err.CodeOf(err), c.want)
			}
		})
	}
}

func TestAuthenticateRejectsWrongRegion(t *testing.T) {
	r := awsGetRequest(t)
	r.Header.Set("Authorization", strings.Replace(r.Header.Get("Authorization"), "/us-east-1/", "/eu-west-1/", 1))
	a := newTestAuthenticator(docNow)
	_, err := a.Authenticate(context.Background(), authRequest(t, r))
	if !s3err.Is(err, s3err.AuthorizationHeaderMalformed) {
		t.Fatalf("err = %v, want AuthorizationHeaderMalformed", err)
	}
}

func TestAuthenticateAnonymous(t *testing.T) {
	r := newHTTPRequest(t, http.MethodGet, "https://bucket.s3.example.com/key", nil)
	a := newTestAuthenticator(docNow)
	res, err := a.Authenticate(context.Background(), authRequest(t, r))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Identity.Anonymous {
		t.Fatalf("identity = %+v, want anonymous", res.Identity)
	}
}

func TestAuthenticateRejectsPresignedParamsWithoutAlgorithm(t *testing.T) {
	r := newHTTPRequest(t, http.MethodGet, "https://bucket.s3.example.com/key?X-Amz-Signature=abc", nil)
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); !s3err.Is(err, s3err.AuthorizationHeaderMalformed) {
		t.Fatalf("err = %v, want AuthorizationHeaderMalformed", err)
	}
}

func TestAuthenticateRejectsHeaderAndPresignTogether(t *testing.T) {
	r := awsGetRequest(t)
	r.URL.RawQuery = "X-Amz-Algorithm=AWS4-HMAC-SHA256"
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); !s3err.Is(err, s3err.AuthorizationHeaderMalformed) {
		t.Fatalf("err = %v, want AuthorizationHeaderMalformed", err)
	}
}

func TestAuthenticateMissingPayloadHashIsRejected(t *testing.T) {
	r := awsGetRequest(t)
	r.Header.Del("X-Amz-Content-Sha256")
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); !s3err.Is(err, s3err.InvalidRequest) {
		t.Fatalf("err = %v, want InvalidRequest", err)
	}
}

func TestAuthenticateStoreFailureIsNotAuthFailure(t *testing.T) {
	boom := errors.New("meta: backend down")
	a := New(failingStore{err: boom}, Options{Region: testRegion, Clock: fixedClock(docNow)})
	_, err := a.Authenticate(context.Background(), authRequest(t, awsGetRequest(t)))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store error", err)
	}
	if s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatal("a backend failure must not be reported as an unknown key")
	}
}

func TestAuthenticateCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(ctx, authRequest(t, awsGetRequest(t))); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDigestMismatchIsReportedAtEOF(t *testing.T) {
	body := []byte("hello")
	wrong := sha256.Sum256([]byte("world"))
	r := io.Reader(newDigestReader(bytes.NewReader(body), wrong))
	if _, err := io.ReadAll(r); !s3err.Is(err, s3err.XAmzContentSHA256Mismatch) {
		t.Fatalf("err = %v, want XAmzContentSHA256Mismatch", err)
	}
}

func TestDigestMatchReturnsEOF(t *testing.T) {
	body := []byte("hello")
	right := sha256.Sum256(body)
	got, err := io.ReadAll(newDigestReader(bytes.NewReader(body), right))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("got %q, %v", got, err)
	}
}

// failingStore fails every lookup with a backend error.
type failingStore struct{ err error }

// Lookup implements CredentialStore.
func (s failingStore) Lookup(context.Context, string) (string, auth.Identity, error) {
	return "", auth.Identity{}, s.err
}

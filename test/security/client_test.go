package security

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// maxResponse caps how much of any response body the suite reads.
const maxResponse = 1 << 20

// httpClient sends raw requests with no retries or redirects.
func httpClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// sdk returns an AWS SDK client for setting up and inspecting state.
func (tg target) sdk(t *testing.T) *s3.Client {
	t.Helper()
	return s3.New(s3.Options{
		Region:       tg.region,
		BaseEndpoint: aws.String(tg.endpoint.String()),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(tg.accessKey, tg.secretKey, ""),
		Retryer:      aws.NopRetryer{},
	})
}

// newBucket creates a uniquely named bucket and removes it, with its
// objects, when the test ends.
func (tg target) newBucket(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	name := "sec-" + randomHex(t, 8)
	c := tg.sdk(t)
	if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(name)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	t.Cleanup(func() {
		p := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{Bucket: aws.String(name)})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				break
			}
			for _, o := range page.Contents {
				_, _ = c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(name), Key: o.Key})
			}
		}
		_, _ = c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(name)})
	})
	return name
}

// objectExists reports whether bucket/key is visible.
func (tg target) objectExists(t *testing.T, bucket, key string) bool {
	t.Helper()
	_, err := tg.sdk(t).HeadObject(context.Background(), &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err == nil {
		return true
	}
	var nf *types.NotFound
	var nsk *types.NoSuchKey
	if errors.As(err, &nf) || errors.As(err, &nsk) || strings.Contains(err.Error(), "StatusCode: 404") {
		return false
	}
	t.Fatalf("head %s/%s: %v", bucket, key, err)
	return false
}

// objectURL returns the path-style URL of bucket/key with an optional query.
func (tg target) objectURL(bucket, key, query string) string {
	u := *tg.endpoint
	u.Path = "/" + bucket
	if key != "" {
		u.Path += "/" + key
	}
	u.RawQuery = query
	return u.String()
}

// creds holds signing credentials, possibly wrong on purpose.
type creds struct {
	accessKey, secretKey string
}

func (tg target) goodCreds() creds { return creds{tg.accessKey, tg.secretKey} }

// sign signs req with SigV4 at time when, using the SHA-256 of body as the
// payload hash unless payloadHash is set.
func (tg target) sign(t *testing.T, req *http.Request, body []byte, c creds, when time.Time, payloadHash string) {
	t.Helper()
	if payloadHash == "" {
		sum := sha256.Sum256(body)
		payloadHash = hex.EncodeToString(sum[:])
	}
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	err := v4.NewSigner().SignHTTP(context.Background(),
		aws.Credentials{AccessKeyID: c.accessKey, SecretAccessKey: c.secretKey},
		req, payloadHash, "s3", tg.region, when)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
}

// newRequest builds a request with an in-memory body.
func newRequest(t *testing.T, method, url string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len(body))
	return req
}

// response is a fully read, size-capped HTTP response.
type response struct {
	status int
	header http.Header
	body   []byte
}

// s3Error is the S3 XML error document.
type s3Error struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func (r response) code() string {
	var e s3Error
	if xml.Unmarshal(r.body, &e) != nil {
		return ""
	}
	return e.Code
}

// send performs req and reads at most maxResponse bytes of the reply.
func send(t *testing.T, req *http.Request) response {
	t.Helper()
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: body}
}

// expectRejected asserts a 4xx status and, when codes are given, one of the
// S3 error codes.
func expectRejected(t *testing.T, r response, codes ...string) {
	t.Helper()
	if r.status < 400 || r.status >= 500 {
		t.Fatalf("expected a 4xx rejection, got %d: %s", r.status, truncate(r.body))
	}
	if len(codes) == 0 {
		return
	}
	got := r.code()
	for _, c := range codes {
		if got == c {
			return
		}
	}
	t.Fatalf("expected error code in %v, got %q (status %d): %s", codes, got, r.status, truncate(r.body))
}

func truncate(b []byte) string {
	const n = 400
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// putObject uploads data with the SDK.
func (tg target) putObject(t *testing.T, bucket, key string, data []byte) {
	t.Helper()
	_, err := tg.sdk(t).PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(data),
	})
	if err != nil {
		t.Fatalf("put %s/%s: %v", bucket, key, err)
	}
}

// getObject downloads an object with the SDK.
func (tg target) getObject(t *testing.T, bucket, key string) []byte {
	t.Helper()
	out, err := tg.sdk(t).GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("get %s/%s: %v", bucket, key, err)
	}
	defer out.Body.Close()
	b, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// mustAlive asserts that the server still serves an authenticated request.
func (tg target) mustAlive(t *testing.T) {
	t.Helper()
	if _, err := tg.sdk(t).ListBuckets(context.Background(), &s3.ListBucketsInput{}); err != nil {
		t.Fatalf("server no longer serves requests: %v", err)
	}
}

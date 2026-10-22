package integration

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// querySignature is the SigV4 query parameter that carries a presigned URL's
// signature.
const querySignature = "X-Amz-Signature"

// rawResponse is the status and body of a request sent outside the SDK.
type rawResponse struct {
	status int
	body   []byte
}

// sendRaw sends one HTTP request with the given method, URL and headers and
// reads the whole response. It bypasses the SDK, so it shows what a plain
// HTTP client sees.
func sendRaw(ctx context.Context, t *testing.T, method, rawURL string, header http.Header, body []byte) rawResponse {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		t.Fatalf("build %s request: %v", method, err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", method, err)
	}
	return rawResponse{status: resp.StatusCode, body: b}
}

// s3ErrorCodeFromXML returns the <Code> element of an S3 XML error body, or ""
// when the body is not one.
func s3ErrorCodeFromXML(body []byte) string {
	var e struct {
		Code string `xml:"Code"`
	}
	if err := xml.Unmarshal(body, &e); err != nil {
		return ""
	}
	return e.Code
}

func TestPresignedPutAndGet(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "presigned/object.bin"
	body := randomBytes(t, 64<<10)
	presigner := s3.NewPresignClient(cli)

	put, err := presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(5*time.Minute))
	if err != nil {
		t.Fatalf("PresignPutObject: %v", err)
	}
	if resp := sendRaw(ctx, t, put.Method, put.URL, put.SignedHeader, body); resp.status != http.StatusOK {
		t.Fatalf("presigned PUT status = %d, body %s", resp.status, resp.body)
	}

	get, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(5*time.Minute))
	if err != nil {
		t.Fatalf("PresignGetObject: %v", err)
	}
	resp := sendRaw(ctx, t, get.Method, get.URL, get.SignedHeader, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("presigned GET status = %d, body %s", resp.status, resp.body)
	}
	if sha256Hex(resp.body) != sha256Hex(body) {
		t.Fatalf("presigned GET body SHA-256 mismatch")
	}
}

func TestPresignedExpired(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "presigned/short-lived"
	putObject(ctx, t, cli, bucket, key, []byte("short"))

	get, err := s3.NewPresignClient(cli).PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(time.Second))
	if err != nil {
		t.Fatalf("PresignGetObject: %v", err)
	}
	// The signing time is no later than the moment PresignGetObject returned.
	// Waiting until two seconds after that point guarantees the one-second
	// expiry has passed on the server, whose clock is this one.
	if err := waitUntil(ctx, time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("waiting for the presigned URL to expire: %v", err)
	}

	resp := sendRaw(ctx, t, get.Method, get.URL, get.SignedHeader, nil)
	if resp.status != http.StatusForbidden {
		t.Fatalf("expired presigned GET status = %d, want 403", resp.status)
	}
	if code := s3ErrorCodeFromXML(resp.body); code != "AccessDenied" {
		t.Errorf("expired presigned GET code = %q, want AccessDenied", code)
	}
}

func TestPresignedTamperedSignature(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "presigned/tampered"
	putObject(ctx, t, cli, bucket, key, []byte("sealed"))

	get, err := s3.NewPresignClient(cli).PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(5*time.Minute))
	if err != nil {
		t.Fatalf("PresignGetObject: %v", err)
	}

	// Change the last character of the signature and keep everything else.
	u, err := url.Parse(get.URL)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	sig := u.Query().Get(querySignature)
	if sig == "" {
		t.Fatalf("presigned URL has no %s parameter", querySignature)
	}
	last := byte('0')
	if sig[len(sig)-1] == '0' {
		last = '1'
	}
	tampered := strings.Replace(get.URL, sig, sig[:len(sig)-1]+string(last), 1)

	resp := sendRaw(ctx, t, get.Method, tampered, get.SignedHeader, nil)
	if resp.status != http.StatusForbidden {
		t.Fatalf("tampered presigned GET status = %d, want 403", resp.status)
	}
	if code := s3ErrorCodeFromXML(resp.body); code != "SignatureDoesNotMatch" {
		t.Errorf("tampered presigned GET code = %q, want SignatureDoesNotMatch", code)
	}
}

package sigv4

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/object"
)

// TestSDKS3UploadWithTrailingChecksum runs the AWS SDK's S3 client against
// a test server. The SDK's own aws-chunked encoder produces the body, and
// the authenticator must verify the request and decode it.
func TestSDKS3UploadWithTrailingChecksum(t *testing.T) {
	payload := patternData(200 << 10)
	a := New(newTestStore(), Options{Region: testRegion})

	type outcome struct {
		body     []byte
		checksum object.Checksum
		hasCks   bool
		mode     string
		err      error
	}
	results := make(chan outcome, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var o outcome
		o.mode = r.Header.Get("X-Amz-Content-Sha256")
		res, err := a.Authenticate(r.Context(), requestOf(r))
		if err != nil {
			o.err = err
			w.WriteHeader(http.StatusForbidden)
			results <- o
			return
		}
		o.body, o.err = io.ReadAll(res.Body)
		if res.Trailer != nil {
			o.checksum, o.hasCks = res.Trailer()
		}
		w.WriteHeader(http.StatusOK)
		results <- o
	}))
	defer srv.Close()

	client := s3.New(s3.Options{
		Region:       testRegion,
		Credentials:  credentials.NewStaticCredentialsProvider(docAccessKey, docSecretKey, ""),
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		HTTPClient:   srv.Client(),
	})
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:            aws.String("examplebucket"),
		Key:               aws.String("sdk/object.bin"),
		Body:              io.MultiReader(bytes.NewReader(payload)),
		ContentLength:     aws.Int64(int64(len(payload))),
		ChecksumAlgorithm: types.ChecksumAlgorithmCrc32c,
	})
	if err != nil {
		t.Fatalf("SDK upload failed: %v", err)
	}
	o := <-results
	if o.err != nil {
		t.Fatalf("server rejected the SDK request (%s): %v", o.mode, o.err)
	}
	if !bytes.Equal(o.body, payload) {
		t.Fatalf("decoded %d bytes, want %d", len(o.body), len(payload))
	}
	t.Logf("payload mode: %s", o.mode)
	if !o.hasCks || o.checksum.Algorithm != object.ChecksumCRC32C || len(o.checksum.Value) != 4 {
		t.Fatalf("trailer checksum = %+v, present %v", o.checksum, o.hasCks)
	}
}

// requestOf converts a server request into the auth.Request view.
func requestOf(r *http.Request) *auth.Request {
	return &auth.Request{
		Method:        r.Method,
		Host:          r.Host,
		RawPath:       r.URL.EscapedPath(),
		RawQuery:      r.URL.RawQuery,
		Header:        r.Header.Clone(),
		Body:          r.Body,
		ContentLength: r.ContentLength,
	}
}

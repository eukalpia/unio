package sigv4

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/auth"
)

// TestAWSDocumentedChunkedUploadSignatures reproduces the chunked upload
// example from the S3 SigV4 documentation. The request is signed by the SDK
// (which produces the seed signature), the body is encoded with the test
// encoder, and the chunk signatures must equal the published values.
func TestAWSDocumentedChunkedUploadSignatures(t *testing.T) {
	r := newHTTPRequest(t, http.MethodPut, "https://s3.amazonaws.com/examplebucket/chunkObject.txt", nil)
	r.Header.Set("Content-Encoding", "aws-chunked")
	r.Header.Set("X-Amz-Decoded-Content-Length", "66560")
	r.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	r.Header.Set("X-Amz-Content-Sha256", streamingPayload)
	chunks := [][]byte{bytes.Repeat([]byte("a"), 65536), bytes.Repeat([]byte("a"), 1024)}
	spec := awsChunkedSpec{
		kind:    payloadStreamingSigned,
		key:     testSigningKey(docSecretKey, "20130524", testRegion),
		amzDate: "20130524T000000Z",
		scope:   "20130524/us-east-1/s3/aws4_request",
		chunks:  chunks,
	}
	r.ContentLength = int64(len(encodeAWSChunked(spec)))
	if r.ContentLength != 66824 {
		t.Fatalf("encoded length = %d, want the documented 66824", r.ContentLength)
	}
	signHeaderWith(t, r, streamingPayload, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	spec.seed = authorizationSeed(t, r.Header.Get("Authorization"))
	wire := encodeAWSChunked(spec)

	want := []string{
		"10000;chunk-signature=ad80c730a21e5b8d04586a2213dd63b9a0e99e0e2307b0ade35a65485a288648",
		"400;chunk-signature=0055627c9e194cb4542bae2aa5492e3c1575bbb81b612b7d234b86a503ef5497",
		"0;chunk-signature=b6c6ea8a5354eaf15b3cb7646744f4275b71ea724fed81ceb9323e279d449df9",
	}
	for _, line := range want {
		if !strings.Contains(string(wire), line) {
			t.Fatalf("chunk header %q not produced by the chain", line)
		}
	}

	// The production path must accept the same request and decode it.
	req := &auth.Request{
		Method:        r.Method,
		Host:          r.Host,
		RawPath:       r.URL.EscapedPath(),
		RawQuery:      r.URL.RawQuery,
		Header:        r.Header.Clone(),
		Body:          bytes.NewReader(wire),
		ContentLength: int64(len(wire)),
	}
	a := newTestAuthenticator(time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	res, err := a.Authenticate(context.Background(), req)
	if err != nil {
		t.Fatalf("documented upload rejected: %v", err)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("documented body rejected: %v", err)
	}
	if !bytes.Equal(got, bytes.Join(chunks, nil)) {
		t.Fatalf("decoded %d bytes, want %d", len(got), 66560)
	}
}

// TestAWSDocumentedGetVectorDetectsCorruption ensures the documented vector
// is sensitive to a one-character change of the date in the scope.
func TestAWSDocumentedGetVectorDetectsCorruption(t *testing.T) {
	r := awsGetRequest(t)
	r.Header.Set("Authorization", strings.Replace(r.Header.Get("Authorization"), "20130524/us-east-1", "20130525/us-east-1", 1))
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); err == nil {
		t.Fatal("a changed credential date was accepted")
	}
}

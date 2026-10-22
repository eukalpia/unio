package sigv4

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/auth"
)

// This file holds a test-only aws-chunked encoder. It is written from the
// AWS framing rules and deliberately shares no code with the decoder under
// test, except the constants that define the wire format.

// testHMAC returns HMAC-SHA256(key, msg).
func testHMAC(key []byte, msg string) [sha256.Size]byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	var out [sha256.Size]byte
	copy(out[:], m.Sum(nil))
	return out
}

// testSigningKey derives the SigV4 signing key for the S3 service.
func testSigningKey(secret, date, region string) [sha256.Size]byte {
	k := testHMAC([]byte("AWS4"+secret), date)
	k = testHMAC(k[:], region)
	k = testHMAC(k[:], "s3")
	return testHMAC(k[:], "aws4_request")
}

// awsChunkedSpec describes an aws-chunked body to encode.
type awsChunkedSpec struct {
	kind    payloadKind
	key     [sha256.Size]byte
	amzDate string
	scope   string
	seed    [sha256.Size]byte
	chunks  [][]byte
	// trailerName and trailerValue describe one checksum trailer. Leave the
	// name empty for no trailer. The value is base64, as sent.
	trailerName  string
	trailerValue string
}

// encodeAWSChunked produces the wire form of the spec.
func encodeAWSChunked(s awsChunkedSpec) []byte {
	signed := s.kind != payloadStreamingUnsignedTrailer
	var out bytes.Buffer
	prev := s.seed
	sign := func(dataHash string) [sha256.Size]byte {
		sts := "AWS4-HMAC-SHA256-PAYLOAD\n" + s.amzDate + "\n" + s.scope + "\n" +
			hex.EncodeToString(prev[:]) + "\n" + emptyPayloadSHA256 + "\n" + dataHash
		return testHMAC(s.key[:], sts)
	}
	for _, data := range s.chunks {
		if signed {
			sum := sha256.Sum256(data)
			sig := sign(hex.EncodeToString(sum[:]))
			fmt.Fprintf(&out, "%x;chunk-signature=%x\r\n", len(data), sig)
			prev = sig
		} else {
			fmt.Fprintf(&out, "%x\r\n", len(data))
		}
		out.Write(data)
		out.WriteString("\r\n")
	}
	if signed {
		sum := sha256.Sum256(nil)
		sig := sign(hex.EncodeToString(sum[:]))
		fmt.Fprintf(&out, "0;chunk-signature=%x\r\n", sig)
		prev = sig
	} else {
		out.WriteString("0\r\n")
	}
	if s.kind == payloadStreamingSigned {
		out.WriteString("\r\n")
		return out.Bytes()
	}
	canon := ""
	if s.trailerName != "" {
		line := s.trailerName + ":" + s.trailerValue
		out.WriteString(line + "\r\n")
		canon = line + "\n"
	}
	if s.kind == payloadStreamingTrailer {
		ch := sha256.Sum256([]byte(canon))
		sts := "AWS4-HMAC-SHA256-TRAILER\n" + s.amzDate + "\n" + s.scope + "\n" +
			hex.EncodeToString(prev[:]) + "\n" + hex.EncodeToString(ch[:])
		sig := testHMAC(s.key[:], sts)
		fmt.Fprintf(&out, "x-amz-trailer-signature:%x\r\n", sig)
	}
	out.WriteString("\r\n")
	return out.Bytes()
}

// streamSpec describes a signed aws-chunked upload to build.
type streamSpec struct {
	marker       string
	trailerName  string
	trailerValue string
	chunks       [][]byte
	extra        http.Header
}

// buildSignedStream signs a PUT with the SDK, using the streaming payload
// marker, and returns the auth.Request whose body is the encoded stream.
// The seed signature comes from the SDK's Authorization header, so the
// request is verified end to end.
func buildSignedStream(t *testing.T, s streamSpec) *auth.Request {
	t.Helper()
	const when = "2013-05-24T00:00:00Z"
	at, err := time.Parse(time.RFC3339, when)
	if err != nil {
		t.Fatal(err)
	}
	var decoded int64
	for _, c := range s.chunks {
		decoded += int64(len(c))
	}
	r := newHTTPRequest(t, http.MethodPut, "https://examplebucket.s3.amazonaws.com/chunkObject.txt", nil)
	r.Header.Set("Content-Encoding", "aws-chunked")
	r.Header.Set("X-Amz-Decoded-Content-Length", strconv.FormatInt(decoded, 10))
	if s.trailerName != "" {
		r.Header.Set("X-Amz-Trailer", s.trailerName)
	}
	for k, vs := range s.extra {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	spec := awsChunkedSpec{
		kind:         kindOf(s.marker),
		key:          testSigningKey(docSecretKey, "20130524", testRegion),
		amzDate:      "20130524T000000Z",
		scope:        "20130524/us-east-1/s3/aws4_request",
		chunks:       s.chunks,
		trailerName:  s.trailerName,
		trailerValue: s.trailerValue,
	}
	// The encoded length does not depend on the signature values, so it can
	// be measured before the request is signed.
	r.ContentLength = int64(len(encodeAWSChunked(spec)))
	r.Header.Set("X-Amz-Content-Sha256", s.marker)
	signHeaderWith(t, r, s.marker, at)
	seed := authorizationSeed(t, r.Header.Get("Authorization"))
	spec.seed = seed
	body := encodeAWSChunked(spec)
	return &auth.Request{
		Method:        r.Method,
		Host:          r.Host,
		RawPath:       r.URL.EscapedPath(),
		RawQuery:      r.URL.RawQuery,
		Header:        r.Header.Clone(),
		Body:          bytes.NewReader(body),
		ContentLength: int64(len(body)),
	}
}

// kindOf maps a streaming marker to its payload kind.
func kindOf(marker string) payloadKind {
	switch marker {
	case streamingPayloadTrailer:
		return payloadStreamingTrailer
	case streamingUnsignedTrailer:
		return payloadStreamingUnsignedTrailer
	}
	return payloadStreamingSigned
}

// signHeaderWith signs r with the given payload marker at the given time.
func signHeaderWith(t *testing.T, r *http.Request, marker string, when time.Time) {
	t.Helper()
	signer := newSDKSigner()
	if err := signer.SignHTTP(context.Background(), sdkCredentials(), r, marker, "s3", testRegion, when); err != nil {
		t.Fatal(err)
	}
}

// authorizationSeed extracts the signature from an Authorization header.
func authorizationSeed(t *testing.T, authz string) [sha256.Size]byte {
	t.Helper()
	i := strings.LastIndex(authz, "Signature=")
	if i < 0 {
		t.Fatalf("no signature in %q", authz)
	}
	raw, err := hex.DecodeString(authz[i+len("Signature="):])
	if err != nil || len(raw) != sha256.Size {
		t.Fatalf("bad signature hex: %v", err)
	}
	var out [sha256.Size]byte
	copy(out[:], raw)
	return out
}

// b64 encodes b as the standard base64 used by checksum trailers.
func b64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

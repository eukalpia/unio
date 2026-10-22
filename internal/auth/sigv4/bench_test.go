package sigv4

import (
	"bytes"
	"context"
	"io"
	"testing"
)

// BenchmarkAuthenticateHeader measures header verification with a warm
// signing-key cache, which is the steady state on a serving node.
func BenchmarkAuthenticateHeader(b *testing.B) {
	req := authRequest(b, awsGetRequest(b))
	a := newTestAuthenticator(docNow)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := a.Authenticate(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChunkDecode measures aws-chunked decoding throughput over 4 MiB
// of 64 KiB signed chunks, read through a 32 KiB buffer.
func BenchmarkChunkDecode(b *testing.B) {
	key := testSigningKey(docSecretKey, "20130524", testRegion)
	const chunkSize = 64 << 10
	const chunkCount = 64
	data := patternData(chunkSize)
	chunks := make([][]byte, chunkCount)
	for i := range chunks {
		chunks[i] = data
	}
	spec := awsChunkedSpec{
		kind: payloadStreamingSigned, key: key, amzDate: "20130524T000000Z",
		scope: "20130524/us-east-1/s3/aws4_request", chunks: chunks,
	}
	wire := encodeAWSChunked(spec)
	cfg := chunkedConfig{
		kind: payloadStreamingSigned, key: key, amzDate: spec.amzDate, scope: spec.scope,
		decodedLen: int64(chunkSize * chunkCount),
	}
	buf := make([]byte, 32<<10)
	b.SetBytes(int64(chunkSize * chunkCount))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		cr := newChunkedReader(bytes.NewReader(wire), cfg)
		for {
			_, err := cr.Read(buf)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkSigningKeyCacheHit measures the cached signing-key lookup.
func BenchmarkSigningKeyCacheHit(b *testing.B) {
	a := newTestAuthenticator(docNow)
	a.signingKey(docSecretKey, "20130524")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		a.signingKey(docSecretKey, "20130524")
	}
}

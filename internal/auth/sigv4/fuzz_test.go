package sigv4

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// FuzzParseAuthorization checks that the header parser never panics and that
// an accepted header round-trips through its canonical form.
func FuzzParseAuthorization(f *testing.F) {
	f.Add("AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41")
	f.Add("AWS4-HMAC-SHA256 Credential=A/20130524/us-east-1/s3/aws4_request,SignedHeaders=host,Signature=00")
	f.Add("AWS4-HMAC-SHA256 SignedHeaders=host, Credential=A/20130524/r/s3/aws4_request, Signature=" + strings.Repeat("0", 64))
	f.Add("")
	f.Add("AWS4-HMAC-SHA256 ,,=,=")
	f.Fuzz(func(t *testing.T, in string) {
		got, err := parseAuthorization(in)
		if err != nil {
			return
		}
		if len(got.signedHeaders) == 0 || !slices.Contains(got.signedHeaders, "host") {
			t.Fatalf("accepted header without a valid host signed header: %q", in)
		}
		rebuilt := algorithm + " Credential=" + got.scope.accessKey + "/" + got.scope.date + "/" +
			got.scope.region + "/s3/aws4_request, SignedHeaders=" + strings.Join(got.signedHeaders, ";") +
			", Signature=" + hex.EncodeToString(got.signature[:])
		again, err := parseAuthorization(rebuilt)
		if err != nil {
			t.Fatalf("canonical form rejected: %q: %v", rebuilt, err)
		}
		if again.scope != got.scope || again.signature != got.signature || strings.Join(again.signedHeaders, ";") != strings.Join(got.signedHeaders, ";") {
			t.Fatalf("round trip changed the header: %q", in)
		}
	})
}

// FuzzCanonicalURI checks that canonicalURI never panics and is idempotent:
// its output is already in canonical form.
func FuzzCanonicalURI(f *testing.F) {
	f.Add("/test.txt")
	f.Add("/a%20b/%E2%82%AC")
	f.Add("/%zz")
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		out, err := canonicalURI(in)
		if err != nil {
			return
		}
		again, err := canonicalURI(out)
		if err != nil || again != out {
			t.Fatalf("not idempotent: %q -> %q -> %q (%v)", in, out, again, err)
		}
	})
}

// FuzzChunkDecoder feeds arbitrary bytes to the signed decoder. It must not
// panic, must never return more than the declared length, and must never
// succeed on data that carries no valid signature chain.
func FuzzChunkDecoder(f *testing.F) {
	key := testSigningKey(docSecretKey, "20130524", testRegion)
	cfg := chunkedConfig{
		kind: payloadStreamingSigned, key: key, amzDate: "20130524T000000Z",
		scope: "20130524/us-east-1/s3/aws4_request", decodedLen: 5,
	}
	valid := encodeAWSChunked(awsChunkedSpec{
		kind: payloadStreamingSigned, key: key, amzDate: cfg.amzDate, scope: cfg.scope,
		chunks: [][]byte{[]byte("hello")},
	})
	f.Add(valid)
	f.Add([]byte("5\r\nhello\r\n0\r\n\r\n"))
	f.Add([]byte("0;chunk-signature=" + strings.Repeat("a", 64) + "\r\n\r\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, in []byte) {
		cr := newChunkedReader(bytes.NewReader(in), cfg)
		out, err := io.ReadAll(cr)
		if int64(len(out)) > cfg.decodedLen {
			t.Fatalf("returned %d bytes, more than the declared %d", len(out), cfg.decodedLen)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.Fatal("failure reported as io.EOF")
			}
			return
		}
		// An accepted stream must be exactly the canonical encoding of its
		// own data, signatures included.
		layout, ok := chunkLayout(in)
		if !ok {
			t.Fatalf("decoder accepted a stream the layout parser cannot read: %q", in)
		}
		want := encodeAWSChunked(awsChunkedSpec{
			kind: payloadStreamingSigned, key: key, amzDate: cfg.amzDate, scope: cfg.scope, chunks: layout,
		})
		if !bytes.Equal(want, in) {
			t.Fatalf("accepted a non-canonical or unsigned stream: %q", in)
		}
	})
}

// chunkLayout returns the data chunks of a signed stream without verifying
// the signatures. It reports false when the framing is not parseable.
func chunkLayout(in []byte) ([][]byte, bool) {
	var chunks [][]byte
	pos := 0
	for {
		nl := bytes.IndexByte(in[pos:], '\n')
		if nl < 0 {
			return nil, false
		}
		header := in[pos : pos+nl+1]
		semi := bytes.IndexByte(header, ';')
		if semi < 0 {
			return nil, false
		}
		size, err := strconv.ParseInt(string(header[:semi]), 16, 64)
		if err != nil || size < 0 {
			return nil, false
		}
		pos += nl + 1
		if size == 0 {
			return chunks, true
		}
		end := pos + int(size)
		if end+2 > len(in) {
			return nil, false
		}
		chunks = append(chunks, in[pos:end])
		pos = end + 2
	}
}

// FuzzChunkTamper encodes a valid signed stream from the fuzzed payload and
// flips one fuzzed byte. Every such change must be detected, and the
// untouched stream must decode to the payload.
func FuzzChunkTamper(f *testing.F) {
	f.Add([]byte("some payload bytes"), uint8(5), uint(7), byte(1))
	f.Add(bytes.Repeat([]byte{0xAB}, 300), uint8(64), uint(0), byte(0x80))
	f.Add([]byte{}, uint8(1), uint(3), byte(0xFF))
	f.Fuzz(func(t *testing.T, payload []byte, split uint8, pos uint, flip byte) {
		if len(payload) > 4096 {
			payload = payload[:4096]
		}
		size := int(split)
		if size == 0 {
			size = 1
		}
		var chunks [][]byte
		for rest := payload; len(rest) > 0; {
			n := min(size, len(rest))
			chunks = append(chunks, rest[:n])
			rest = rest[n:]
		}
		key := testSigningKey(docSecretKey, "20130524", testRegion)
		seed := sha256.Sum256([]byte("fuzz seed"))
		spec := awsChunkedSpec{
			kind: payloadStreamingSigned, key: key, amzDate: "20130524T000000Z",
			scope: "20130524/us-east-1/s3/aws4_request", seed: seed, chunks: chunks,
		}
		wire := encodeAWSChunked(spec)
		cfg := chunkedConfig{
			kind: payloadStreamingSigned, key: key, amzDate: spec.amzDate, scope: spec.scope,
			seed: seed, decodedLen: int64(len(payload)),
		}
		got, err := readStream(wire, cfg)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("valid stream did not round-trip: %v", err)
		}
		if flip == 0 || len(wire) == 0 {
			return
		}
		tampered := bytes.Clone(wire)
		tampered[pos%uint(len(tampered))] ^= flip
		if _, err := readStream(tampered, cfg); err == nil {
			t.Fatalf("tampered stream accepted (byte %d ^ %#x)", pos%uint(len(wire)), flip)
		}
	})
}

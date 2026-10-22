package sigv4

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// patternData returns n deterministic, non-repeating bytes.
func patternData(n int) []byte {
	b := make([]byte, n)
	var x uint32 = 2463534242
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

// streamingCases lists the streaming modes with trailers and chunk layouts.
var streamingCases = []struct {
	name         string
	marker       string
	trailerName  string
	trailerValue string
	chunks       [][]byte
}{
	{"signed one chunk", streamingPayload, "", "", [][]byte{[]byte("hello world")}},
	{"signed two chunks", streamingPayload, "", "", [][]byte{patternData(65536), patternData(1024)}},
	{"signed many small chunks", streamingPayload, "", "", [][]byte{[]byte("a"), []byte("bc"), patternData(7), []byte("z")}},
	{"signed trailer crc32c", streamingPayloadTrailer, "x-amz-checksum-crc32c", b64([]byte{1, 2, 3, 4}), [][]byte{patternData(70000)}},
	{"signed trailer sha256", streamingPayloadTrailer, "x-amz-checksum-sha256", b64(make([]byte, 32)), [][]byte{patternData(300), patternData(5)}},
	{"unsigned trailer crc64nvme", streamingUnsignedTrailer, "x-amz-checksum-crc64nvme", b64(make([]byte, 8)), [][]byte{patternData(65536), patternData(3)}},
	{"unsigned trailer no checksum", streamingUnsignedTrailer, "", "", [][]byte{patternData(10)}},
}

func TestStreamingRoundTrip(t *testing.T) {
	for _, c := range streamingCases {
		t.Run(c.name, func(t *testing.T) {
			req := buildSignedStream(t, streamSpec{
				marker: c.marker, trailerName: c.trailerName, trailerValue: c.trailerValue, chunks: c.chunks,
			})
			a := newTestAuthenticator(time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
			res, err := a.Authenticate(context.Background(), req)
			if err != nil {
				t.Fatalf("authenticate: %v", err)
			}
			got, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			want := bytes.Join(c.chunks, nil)
			if !bytes.Equal(got, want) {
				t.Fatalf("decoded %d bytes, want %d", len(got), len(want))
			}
			if res.DecodedLength != int64(len(want)) {
				t.Fatalf("DecodedLength = %d, want %d", res.DecodedLength, len(want))
			}
			if c.trailerName == "" {
				if _, ok := res.Trailer(); ok {
					t.Fatal("unexpected trailer checksum")
				}
				return
			}
			cks, ok := res.Trailer()
			if !ok || cks.Algorithm == object.ChecksumNone {
				t.Fatal("trailer checksum missing")
			}
			if want := mustBase64(t, c.trailerValue); !bytes.Equal(cks.Value, want) {
				t.Fatalf("trailer value = %x, want %x", cks.Value, want)
			}
		})
	}
}

func TestStreamingReadsInSmallPieces(t *testing.T) {
	chunks := [][]byte{patternData(1000), patternData(9)}
	spec := awsChunkedSpec{
		kind: payloadStreamingSigned, key: testSigningKey(docSecretKey, "20130524", testRegion),
		amzDate: "20130524T000000Z", scope: "20130524/us-east-1/s3/aws4_request", chunks: chunks,
	}
	seed := sha256.Sum256([]byte("seed"))
	spec.seed = seed
	wire := encodeAWSChunked(spec)
	want := bytes.Join(chunks, nil)
	for _, size := range []int{1, 7, 64, 4096} {
		cr := newChunkedReader(iotest.OneByteReader(bytes.NewReader(wire)), chunkedConfig{
			kind: payloadStreamingSigned, key: spec.key, amzDate: spec.amzDate, scope: spec.scope,
			seed: seed, decodedLen: int64(len(want)),
		})
		var got []byte
		buf := make([]byte, size)
		for {
			n, err := cr.Read(buf)
			got = append(got, buf[:n]...)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("size %d: %v", size, err)
			}
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("size %d: decoded data differs", size)
		}
	}
}

func TestStreamingEmptyPayload(t *testing.T) {
	req := buildSignedStream(t, streamSpec{marker: streamingPayload})
	a := newTestAuthenticator(time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	res, err := a.Authenticate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d bytes, err %v", len(got), err)
	}
}

// tamperCase mutates a valid encoded stream.
type tamperCase struct {
	name   string
	mutate func(wire []byte) []byte
	want   s3err.Code
}

// signedWire returns the encoded signed payload stream used by tamper tests.
func signedWire(t *testing.T) (wire []byte, cfg chunkedConfig) {
	t.Helper()
	key := testSigningKey(docSecretKey, "20130524", testRegion)
	seed := sha256.Sum256([]byte("seed"))
	chunks := [][]byte{[]byte("first chunk data"), []byte("second chunk")}
	spec := awsChunkedSpec{
		kind: payloadStreamingSigned, key: key, amzDate: "20130524T000000Z",
		scope: "20130524/us-east-1/s3/aws4_request", seed: seed, chunks: chunks,
	}
	cfg = chunkedConfig{
		kind: payloadStreamingSigned, key: key, amzDate: spec.amzDate, scope: spec.scope,
		seed: seed, decodedLen: int64(len(bytes.Join(chunks, nil))),
	}
	return encodeAWSChunked(spec), cfg
}

// readStream decodes wire with cfg and returns the first error.
func readStream(wire []byte, cfg chunkedConfig) ([]byte, error) {
	cr := newChunkedReader(bytes.NewReader(wire), cfg)
	return io.ReadAll(cr)
}

func TestStreamingTamperIsRejected(t *testing.T) {
	wire, cfg := signedWire(t)
	cases := []tamperCase{
		{"data byte flipped", func(w []byte) []byte {
			i := bytes.Index(w, []byte("second"))
			out := bytes.Clone(w)
			out[i] ^= 0x01
			return out
		}, s3err.SignatureDoesNotMatch},
		{"first chunk byte flipped", func(w []byte) []byte {
			out := bytes.Clone(w)
			i := bytes.Index(out, []byte("first"))
			out[i] = 'F'
			return out
		}, s3err.SignatureDoesNotMatch},
		{"chunk signature flipped", func(w []byte) []byte {
			out := bytes.Clone(w)
			i := bytes.Index(out, []byte("chunk-signature="))
			out[i+len("chunk-signature=")] ^= 0x01
			return out
		}, s3err.SignatureDoesNotMatch},
		{"final chunk removed", func(w []byte) []byte {
			i := bytes.LastIndex(w, []byte("0;chunk-signature="))
			return w[:i]
		}, s3err.IncompleteBody},
		{"stream cut mid data", func(w []byte) []byte {
			return w[:bytes.Index(w, []byte("second"))+3]
		}, s3err.IncompleteBody},
		{"trailing data after final chunk", func(w []byte) []byte {
			return append(bytes.Clone(w), []byte("x")...)
		}, s3err.InvalidRequest},
		{"chunks reordered", func(w []byte) []byte {
			s := splitSegments(t, w)
			return bytes.Join([][]byte{s[1], s[0], s[2]}, nil)
		}, s3err.SignatureDoesNotMatch},
		{"middle chunk dropped", func(w []byte) []byte {
			s := splitSegments(t, w)
			return bytes.Join([][]byte{s[0], s[2]}, nil)
		}, s3err.SignatureDoesNotMatch},
		{"missing CRLF after data", func(w []byte) []byte {
			i := bytes.Index(w, []byte("first chunk data"))
			out := bytes.Clone(w)
			return append(out[:i+len("first chunk data")], out[i+len("first chunk data")+2:]...)
		}, s3err.InvalidRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := readStream(c.mutate(wire), cfg)
			if err == nil {
				t.Fatalf("tampered stream accepted, %d bytes", len(got))
			}
			if errors.Is(err, io.EOF) {
				t.Fatal("failure reported as io.EOF")
			}
			if !s3err.Is(err, c.want) {
				t.Fatalf("err = %v, want %s", err, c.want)
			}
		})
	}
}

func TestStreamingErrorIsSticky(t *testing.T) {
	wire, cfg := signedWire(t)
	out := bytes.Clone(wire)
	out[bytes.Index(out, []byte("second"))] ^= 0x01
	cr := newChunkedReader(bytes.NewReader(out), cfg)
	_, first := io.ReadAll(cr)
	if first == nil {
		t.Fatal("expected an error")
	}
	for i := range 3 {
		n, err := cr.Read(make([]byte, 16))
		if n != 0 || err == nil || err.Error() != first.Error() {
			t.Fatalf("read %d after failure = (%d, %v), want (0, %v)", i, n, err, first)
		}
	}
}

func TestStreamingWrongSeedIsRejected(t *testing.T) {
	wire, cfg := signedWire(t)
	cfg.seed = sha256.Sum256([]byte("another seed"))
	if _, err := readStream(wire, cfg); !s3err.Is(err, s3err.SignatureDoesNotMatch) {
		t.Fatalf("err = %v, want SignatureDoesNotMatch", err)
	}
}

func TestStreamingDecodedLengthIsEnforced(t *testing.T) {
	wire, cfg := signedWire(t)
	cfg.decodedLen++
	if _, err := readStream(wire, cfg); !s3err.Is(err, s3err.IncompleteBody) {
		t.Fatalf("too long declared: err = %v, want IncompleteBody", err)
	}
	cfg.decodedLen -= 5
	if _, err := readStream(wire, cfg); !s3err.Is(err, s3err.InvalidRequest) {
		t.Fatalf("too short declared: err = %v, want InvalidRequest", err)
	}
}

// unsignedFraming feeds raw framing to the unsigned trailer decoder.
func unsignedFraming(t *testing.T, body string, declared []string, decodedLen int64) error {
	t.Helper()
	cr := newChunkedReader(strings.NewReader(body), chunkedConfig{
		kind: payloadStreamingUnsignedTrailer, decodedLen: decodedLen, declared: declared,
	})
	_, err := io.ReadAll(cr)
	return err
}

func TestAWSChunkFramingRejects(t *testing.T) {
	big := fmt.Sprintf("%x", maxChunkSize+1)
	cases := []struct {
		name    string
		body    string
		decoded int64
		want    s3err.Code
	}{
		{"uppercase hex", "A\r\n0123456789\r\n0\r\n\r\n", 3, s3err.InvalidRequest},
		{"0x prefix", "0x3\r\nabc\r\n0\r\n\r\n", 3, s3err.InvalidRequest},
		{"plus sign", "+3\r\nabc\r\n0\r\n\r\n", 3, s3err.InvalidRequest},
		{"leading zero", "03\r\nabc\r\n0\r\n\r\n", 3, s3err.InvalidRequest},
		{"too many digits", "000000003\r\nabc\r\n0\r\n\r\n", 3, s3err.InvalidRequest},
		{"oversized chunk", big + "\r\n", 3, s3err.InvalidRequest},
		{"bare LF", "3\nabc\r\n0\r\n\r\n", 3, s3err.InvalidRequest},
		{"chunk extension in unsigned mode", "3;foo=bar\r\nabc\r\n0\r\n\r\n", 3, s3err.InvalidRequest},
		{"missing CRLF after data", "3\r\nabcXY0\r\n\r\n", 3, s3err.InvalidRequest},
		{"overlong header line", strings.Repeat("3", 400) + "\r\n", 3, s3err.InvalidRequest},
		{"unknown trailer", "0\r\nx-amz-foo:bar\r\n\r\n", 0, s3err.InvalidRequest},
		{"trailer without CRLF", "0\r\nx-amz-checksum-crc32c:AAAAAA==\n\r\n", 0, s3err.InvalidRequest},
		{"trailer wrong digest length", "0\r\nx-amz-checksum-crc32c:AAAAAAAA\r\n\r\n", 0, s3err.InvalidRequest},
		{"trailer bad base64", "0\r\nx-amz-checksum-crc32c:!!!!\r\n\r\n", 0, s3err.InvalidRequest},
		{"two checksums", "0\r\nx-amz-checksum-crc32c:AAAAAA==\r\nx-amz-checksum-sha1:AAAAAAAAAAAAAAAAAAAAAAAAAAA=\r\n\r\n", 0, s3err.InvalidRequest},
		{"trailer without terminating blank line", "0\r\nx-amz-checksum-crc32c:AAAAAA==\r\n", 0, s3err.IncompleteBody},
		{"unexpected trailer signature", "0\r\nx-amz-trailer-signature:" + strings.Repeat("a", 64) + "\r\n\r\n", 0, s3err.InvalidRequest},
		{"data after the final chunk", "0\r\n\r\nxyz", 0, s3err.InvalidRequest},
		{"empty body", "", 3, s3err.IncompleteBody},
		{"truncated size line", "3", 3, s3err.IncompleteBody},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := unsignedFraming(t, c.body, nil, c.decoded)
			if !s3err.Is(err, c.want) {
				t.Fatalf("err = %v, want %s", err, c.want)
			}
		})
	}
}

func TestAWSChunkFramingAcceptsCanonicalForms(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		declared []string
		decoded  int64
	}{
		{"one chunk and terminator", "3\r\nabc\r\n0\r\n\r\n", nil, 3},
		{"empty payload", "0\r\n\r\n", nil, 0},
		{"checksum trailer", "3\r\nabc\r\n0\r\nx-amz-checksum-crc32c:AAAAAA==\r\n\r\n", []string{"x-amz-checksum-crc32c"}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := unsignedFraming(t, c.body, c.declared, c.decoded); err != nil {
				t.Fatalf("rejected canonical framing: %v", err)
			}
		})
	}
}

func TestAWSChunkDeclaredTrailerMustMatch(t *testing.T) {
	body := "0\r\nx-amz-checksum-crc32c:AAAAAA==\r\n\r\n"
	if err := unsignedFraming(t, body, []string{"x-amz-checksum-sha1"}, 0); !s3err.Is(err, s3err.InvalidRequest) {
		t.Fatalf("mismatched announcement: err = %v", err)
	}
	if err := unsignedFraming(t, "0\r\n\r\n", []string{"x-amz-checksum-crc32c"}, 0); !s3err.Is(err, s3err.InvalidRequest) {
		t.Fatalf("announced trailer missing: err = %v", err)
	}
}

func TestUnsignedTrailerChecksumIsExposed(t *testing.T) {
	body := "3\r\nabc\r\n0\r\nx-amz-checksum-crc32c:AQIDBA==\r\n\r\n"
	cr := newChunkedReader(strings.NewReader(body), chunkedConfig{
		kind: payloadStreamingUnsignedTrailer, decodedLen: 3, declared: []string{"x-amz-checksum-crc32c"},
	})
	if _, ok := cr.trailerChecksum(); ok {
		t.Fatal("trailer visible before EOF")
	}
	got, err := io.ReadAll(cr)
	if err != nil || string(got) != "abc" {
		t.Fatalf("got %q, %v", got, err)
	}
	cks, ok := cr.trailerChecksum()
	if !ok || cks.Algorithm != object.ChecksumCRC32C || !bytes.Equal(cks.Value, []byte{1, 2, 3, 4}) {
		t.Fatalf("checksum = %+v, ok %v", cks, ok)
	}
}

// mustBase64 decodes a standard base64 test value.
func mustBase64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// splitSegments splits a signed wire stream into its chunk segments. Each
// data segment holds the header, the data and the CRLF; the last segment is
// the final chunk with its trailing CRLF.
func splitSegments(t *testing.T, wire []byte) [][]byte {
	t.Helper()
	var segs [][]byte
	pos := 0
	for {
		nl := bytes.IndexByte(wire[pos:], '\n')
		if nl < 0 {
			t.Fatal("unterminated chunk header")
		}
		header := wire[pos : pos+nl+1]
		size, err := strconv.ParseInt(string(header[:bytes.IndexByte(header, ';')]), 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		if size == 0 {
			segs = append(segs, wire[pos:])
			return segs
		}
		end := pos + nl + 1 + int(size) + 2
		segs = append(segs, wire[pos:end])
		pos = end
	}
}

package sigv4

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// These tests fix how trailer lines are read. The aws-sdk-go-v2 encoder
// writes exactly "name:value\r\n" with a lower-case name and no whitespace,
// and the SDK interop test covers that form. HTTP field rules are looser:
// names are case-insensitive, and optional whitespace around a value is
// insignificant. The variants below check both.

// trailerValueB64 is the base64 of the four bytes 1, 2, 3, 4.
const trailerValueB64 = "AQIDBA=="

// trailerCase is one way of writing a checksum trailer line.
type trailerCase struct {
	name     string
	wireName string
	before   string
	after    string
	// value overrides the checksum value when set.
	value string
	// ok is the expected outcome. When it is false, code is the expected
	// error code.
	ok   bool
	code s3err.Code
}

// trailerCases lists the accepted and refused line forms. Each runs under
// both trailer modes.
var trailerCases = []trailerCase{
	{name: "sdk form", ok: true},
	{name: "space after colon", before: " ", ok: true},
	{name: "space before line end", after: " ", ok: true},
	{name: "several spaces", before: "   ", after: "  ", ok: true},
	{name: "tabs around value", before: "\t", after: "\t", ok: true},
	{name: "upper-case name", wireName: "X-AMZ-CHECKSUM-CRC32C", ok: true},
	{name: "mixed-case name", wireName: "X-Amz-Checksum-Crc32C", before: " ", ok: true},
	{name: "space before colon", wireName: "x-amz-checksum-crc32c ", code: s3err.InvalidRequest},
	{name: "space inside value", value: "AQID BA==", code: s3err.InvalidRequest},
	{name: "empty value", value: "", code: s3err.InvalidRequest},
	{name: "whitespace-only value", value: "", before: "   ", code: s3err.InvalidRequest},
}

// trailerModes are the two trailer payload modes.
var trailerModes = []struct {
	name   string
	marker string
}{
	{"signed trailer", streamingPayloadTrailer},
	{"unsigned trailer", streamingUnsignedTrailer},
}

// trailerOutcome authenticates a stream built with buildSignedStream and reads
// it to the end. It returns the decoded data, the trailing checksum, and the
// first error from either step.
func trailerOutcome(t *testing.T, s streamSpec) ([]byte, object.Checksum, bool, error) {
	t.Helper()
	req := buildSignedStream(t, s)
	res, err := newTestAuthenticator(docNow).Authenticate(context.Background(), req)
	if err != nil {
		return nil, object.Checksum{}, false, err
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, object.Checksum{}, false, err
	}
	cks, ok := res.Trailer()
	return data, cks, ok, nil
}

func TestTrailerLineVariants(t *testing.T) {
	for _, mode := range trailerModes {
		for _, c := range trailerCases {
			t.Run(mode.name+"/"+c.name, func(t *testing.T) {
				value := c.value
				if value == "" && c.code == "" {
					value = trailerValueB64
				}
				spec := streamSpec{
					marker:       mode.marker,
					trailerName:  "x-amz-checksum-crc32c",
					trailerValue: value,
					wireName:     c.wireName,
					padBefore:    c.before,
					padAfter:     c.after,
					chunks:       [][]byte{[]byte("hello")},
				}
				data, cks, ok, err := trailerOutcome(t, spec)
				if !c.ok {
					if !s3err.Is(err, c.code) {
						t.Fatalf("err = %v, want %s", err, c.code)
					}
					return
				}
				if err != nil {
					t.Fatalf("accepted form rejected: %v", err)
				}
				if string(data) != "hello" {
					t.Fatalf("data = %q", data)
				}
				if !ok || cks.Algorithm != object.ChecksumCRC32C || !bytes.Equal(cks.Value, []byte{1, 2, 3, 4}) {
					t.Fatalf("trailer = %+v, present %v", cks, ok)
				}
			})
		}
	}
}

// TestSignedTrailerSignsTrimmedValue checks the canonical form of a signed
// trailer. The signer canonicalizes the lower-case name and the trimmed
// value, as it does for headers. A signature over the padded value is
// refused.
func TestSignedTrailerSignsTrimmedValue(t *testing.T) {
	base := streamSpec{
		marker:       streamingPayloadTrailer,
		trailerName:  "x-amz-checksum-crc32c",
		trailerValue: trailerValueB64,
		padBefore:    " ",
		padAfter:     "\t",
		chunks:       [][]byte{[]byte("hello")},
	}
	if _, _, _, err := trailerOutcome(t, base); err != nil {
		t.Fatalf("trimmed canonical value rejected: %v", err)
	}
	padded := base
	padded.canonPadded = true
	if _, _, _, err := trailerOutcome(t, padded); !s3err.Is(err, s3err.SignatureDoesNotMatch) {
		t.Fatalf("padded canonical value: err = %v, want SignatureDoesNotMatch", err)
	}
}

// TestTrailerSignatureNameIsCaseInsensitive checks that the signature line is
// recognized in any case, as the other trailer field names are.
func TestTrailerSignatureNameIsCaseInsensitive(t *testing.T) {
	spec := streamSpec{
		marker:       streamingPayloadTrailer,
		trailerName:  "x-amz-checksum-crc32c",
		trailerValue: trailerValueB64,
		chunks:       [][]byte{[]byte("hello")},
	}
	req := buildSignedStream(t, spec)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	upper := bytes.Replace(body, []byte("x-amz-trailer-signature:"), []byte("X-Amz-Trailer-Signature:"), 1)
	if bytes.Equal(upper, body) {
		t.Fatal("test stream has no signature line to rename")
	}
	req.Body = bytes.NewReader(upper)
	req.ContentLength = int64(len(upper))
	res, err := newTestAuthenticator(docNow).Authenticate(context.Background(), req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if _, err := io.ReadAll(res.Body); err != nil {
		t.Fatalf("upper-case signature name rejected: %v", err)
	}
}

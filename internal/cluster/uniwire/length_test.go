package uniwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// TestLengthU32Boundaries checks the uint32 length encoder at its limit, one
// past it, and at the edges of the field.
func TestLengthU32Boundaries(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		limit int
		ok    bool
		want  uint32
	}{
		{name: "zero", n: 0, limit: 0, ok: true, want: 0},
		{name: "at limit", n: 4096, limit: 4096, ok: true, want: 4096},
		{name: "one over limit", n: 4097, limit: 4096, ok: false},
		{name: "negative", n: -1, limit: 4096, ok: false},
		{name: "negative with no limit", n: -1, limit: math.MaxInt, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lengthU32(tc.n, tc.limit)
			if (err == nil) != tc.ok {
				t.Fatalf("lengthU32(%d, %d) error = %v, want ok=%v", tc.n, tc.limit, err, tc.ok)
			}
			if err == nil && got != tc.want {
				t.Fatalf("lengthU32(%d) = %d, want %d", tc.n, got, tc.want)
			}
		})
	}
}

// TestLengthU32FieldMaximum checks the edge of the uint32 field itself. It runs
// only where int holds a 32-bit value, so the constant below fits.
func TestLengthU32FieldMaximum(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("the field edge is above MaxInt on this platform")
	}
	edge := uint32(math.MaxUint32)
	got, err := lengthU32(int(edge), math.MaxInt)
	if err != nil || got != edge {
		t.Fatalf("maximum field value = %d, %v", got, err)
	}
	past := int(edge) + 1
	if _, err := lengthU32(past, math.MaxInt); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("value past the field = %v, want errFrameTooLarge", err)
	}
}

// TestLengthU8Boundaries checks the uint8 length encoder for method names. A
// name of 255 bytes is the longest that fits.
func TestLengthU8Boundaries(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		limit int
		ok    bool
		want  uint8
	}{
		{name: "empty", n: 0, limit: maxMethodLen, ok: true, want: 0},
		{name: "longest method", n: maxMethodLen, limit: maxMethodLen, ok: true, want: maxMethodLen},
		{name: "one past the method limit", n: maxMethodLen + 1, limit: maxMethodLen, ok: false},
		{name: "past the field even with a large limit", n: 256, limit: 1 << 20, ok: false},
		{name: "negative", n: -1, limit: maxMethodLen, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lengthU8(tc.n, tc.limit)
			if (err == nil) != tc.ok {
				t.Fatalf("lengthU8(%d, %d) error = %v, want ok=%v", tc.n, tc.limit, err, tc.ok)
			}
			if err == nil && got != tc.want {
				t.Fatalf("lengthU8(%d) = %d, want %d", tc.n, got, tc.want)
			}
		})
	}
}

// TestRPCFrameBoundaries feeds the encoder a body of exactly the limit and of
// one byte over it, and a method name of exactly the limit and one over it.
func TestRPCFrameBoundaries(t *testing.T) {
	const max = 64
	// A one-byte method makes the fixed part rpcFixedLen+1 bytes, so a payload
	// of max-rpcFixedLen-1 bytes gives a body of exactly max.
	payload := make([]byte, max-rpcFixedLen-1)
	frame, err := appendRPCFrame(nil, max, frameRequest, 1, "m", payload)
	if err != nil {
		t.Fatalf("body at the limit rejected: %v", err)
	}
	if got := binary.BigEndian.Uint32(frame[:4]); got != max {
		t.Fatalf("length field = %d, want %d", got, max)
	}
	if _, err := appendRPCFrame(nil, max, frameRequest, 1, "m", append(payload, 0)); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("body one byte over the limit = %v, want errFrameTooLarge", err)
	}

	longest := strings.Repeat("m", maxMethodLen)
	if _, err := appendRPCFrame(nil, 1<<20, frameRequest, 1, longest); err != nil {
		t.Fatalf("method at the limit rejected: %v", err)
	}
	if _, err := appendRPCFrame(nil, 1<<20, frameRequest, 1, longest+"m"); err == nil {
		t.Fatal("method one byte over the limit accepted")
	}
}

// TestRPCDecoderAtLengthBoundary decodes frames whose length field sits exactly
// on the limit and one past it.
func TestRPCDecoderAtLengthBoundary(t *testing.T) {
	const max = 64
	// A response with no method has a body of rpcFixedLen plus its payload, so
	// this payload gives a body of exactly max.
	atLimit, err := appendRPCFrame(nil, max, frameResponse, 9, "", make([]byte, max-rpcFixedLen))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readRPCFrame(bytes.NewReader(atLimit), max); err != nil {
		t.Fatalf("frame at the limit rejected: %v", err)
	}
	// A length field one past the limit is refused before the body is read.
	over := binary.BigEndian.AppendUint32(nil, max+1)
	over = append(over, make([]byte, max+1)...)
	if _, err := readRPCFrame(bytes.NewReader(over), max); !errors.Is(err, errMalformedFrame) {
		t.Fatalf("length one over the limit = %v, want errMalformedFrame", err)
	}
}

// TestMessageFrameBoundaries encodes and decodes a protobuf message whose
// encoding is exactly the limit, and one whose encoding is one byte over.
func TestMessageFrameBoundaries(t *testing.T) {
	msg := &uniov1.StreamHeader{Method: "drive.WriteShard", Payload: []byte("p")}
	size, err := msg.MarshalVT()
	if err != nil {
		t.Fatal(err)
	}
	exact := len(size)
	if _, err := appendMessageFrame(nil, exact, msg); err != nil {
		t.Fatalf("message at the limit rejected: %v", err)
	}
	if _, err := appendMessageFrame(nil, exact-1, msg); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("message one byte over the limit = %v, want errFrameTooLarge", err)
	}

	enc, err := appendMessageFrame(nil, exact, msg)
	if err != nil {
		t.Fatal(err)
	}
	var out uniov1.StreamHeader
	if err := readMessageFrame(bytes.NewReader(enc), exact, &out); err != nil {
		t.Fatalf("decode at the limit: %v", err)
	}
	if err := readMessageFrame(bytes.NewReader(enc), exact-1, &out); !errors.Is(err, errMalformedFrame) {
		t.Fatalf("decode one byte over the limit = %v, want errMalformedFrame", err)
	}
}

// TestMessageDecoderRejectsNegativeLength feeds a length with its top bit set.
// On 32-bit platforms that length is negative, and it must not reach make.
func TestMessageDecoderRejectsNegativeLength(t *testing.T) {
	in := binary.BigEndian.AppendUint32(nil, 0x80000000)
	var out uniov1.StreamHeader
	if err := readMessageFrame(bytes.NewReader(in), math.MaxInt32, &out); !errors.Is(err, errMalformedFrame) {
		t.Fatalf("length with the top bit set = %v, want errMalformedFrame", err)
	}
}

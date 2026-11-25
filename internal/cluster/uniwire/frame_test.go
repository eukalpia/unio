package uniwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

func TestRPCFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		typ     frameType
		id      uint64
		method  string
		payload []byte
	}{
		{name: "request", typ: frameRequest, id: 1, method: "meta.Apply", payload: []byte("body")},
		{name: "request empty payload", typ: frameRequest, id: 2, method: "x", payload: nil},
		{name: "response", typ: frameResponse, id: 1 << 40, payload: []byte{0, 1, 2}},
		{name: "error", typ: frameError, id: 9, payload: []byte("err")},
		{name: "cancel", typ: frameCancel, id: 77},
		{name: "ping", typ: framePing},
		{name: "pong", typ: framePong},
		{name: "longest method", typ: frameRequest, id: 3, method: strings.Repeat("m", maxMethodLen)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := appendRPCFrame(nil, 1<<20, tc.typ, tc.id, tc.method, tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			got, err := readRPCFrame(bytes.NewReader(enc), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if got.typ != tc.typ || got.id != tc.id || got.method != tc.method || !bytes.Equal(got.payload, tc.payload) {
				t.Fatalf("round trip mismatch: got %+v", got)
			}
		})
	}
}

func TestAppendRPCFrameLimits(t *testing.T) {
	cases := []struct {
		name   string
		max    int
		method string
		body   []byte
	}{
		{name: "method too long", max: 1 << 20, method: strings.Repeat("m", maxMethodLen+1)},
		{name: "body over limit", max: 64, method: "m", body: make([]byte, 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := appendRPCFrame(nil, tc.max, frameRequest, 1, tc.method, tc.body); err == nil {
				t.Fatal("oversize frame encoded")
			}
		})
	}
}

func TestReadRPCFrameRejectsMalformed(t *testing.T) {
	// header builds a frame whose length field is n and whose body is body.
	header := func(n uint32, body ...byte) []byte {
		b := binary.BigEndian.AppendUint32(nil, n)
		return append(b, body...)
	}
	const max = 1024
	cases := []struct {
		name string
		in   []byte
	}{
		{name: "empty", in: nil},
		{name: "short length prefix", in: []byte{0, 0}},
		{name: "length below fixed part", in: header(rpcFixedLen - 1)},
		{name: "length above limit", in: header(max + 1)},
		{name: "huge length, no body", in: header(0xFFFFFFF0)},
		{name: "truncated body", in: header(20, make([]byte, 5)...)},
		{name: "method overruns body", in: header(10, append(binary.BigEndian.AppendUint64(nil, 1), byte(frameRequest), 200)...)},
		{name: "unknown type", in: header(10, append(binary.BigEndian.AppendUint64(nil, 1), 99, 0)...)},
		{name: "response carrying method", in: header(12, append(binary.BigEndian.AppendUint64(nil, 1), byte(frameResponse), 2, 'a', 'b')...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readRPCFrame(bytes.NewReader(tc.in), max); err == nil {
				t.Fatal("malformed frame accepted")
			}
		})
	}
}

func TestReadRPCFrameLengthErrorIsMalformed(t *testing.T) {
	in := binary.BigEndian.AppendUint32(nil, 1<<30)
	_, err := readRPCFrame(bytes.NewReader(in), 1024)
	if !errors.Is(err, errMalformedFrame) {
		t.Fatalf("error = %v, want errMalformedFrame", err)
	}
}

func TestMessageFrameRoundTrip(t *testing.T) {
	in := &uniov1.StreamHeader{Id: 42, Method: "drive.WriteShard", Payload: []byte("p"), Length: 1 << 30, TimeoutNanos: 5}
	enc, err := appendMessageFrame(nil, 1<<20, in)
	if err != nil {
		t.Fatal(err)
	}
	var out uniov1.StreamHeader
	if err := readMessageFrame(bytes.NewReader(enc), 1<<20, &out); err != nil {
		t.Fatal(err)
	}
	if out.Id != in.Id || out.Method != in.Method || !bytes.Equal(out.Payload, in.Payload) || out.Length != in.Length || out.TimeoutNanos != in.TimeoutNanos {
		t.Fatalf("round trip mismatch: %v", &out)
	}
}

func TestMessageFrameLimits(t *testing.T) {
	big := &uniov1.StreamHeader{Payload: make([]byte, 200)}
	if _, err := appendMessageFrame(nil, 100, big); err == nil {
		t.Fatal("oversize message encoded")
	}
	in := binary.BigEndian.AppendUint32(nil, 1<<29)
	var out uniov1.StreamHeader
	if err := readMessageFrame(bytes.NewReader(in), 1024, &out); !errors.Is(err, errMalformedFrame) {
		t.Fatalf("oversize message read: %v, want errMalformedFrame", err)
	}
	garbage := append(binary.BigEndian.AppendUint32(nil, 3), 0xff, 0xff, 0xff)
	if err := readMessageFrame(bytes.NewReader(garbage), 1024, &out); err == nil {
		t.Fatal("undecodable message accepted")
	}
}

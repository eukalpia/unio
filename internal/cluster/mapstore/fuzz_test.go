package mapstore

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/mappb"
)

// FuzzDecode feeds arbitrary bytes to the file decoder. It must never panic,
// every rejection must wrap ErrCorrupt, and an accepted file must survive a
// round trip.
func FuzzDecode(f *testing.F) {
	valid, err := encode(testMap())
	if err != nil {
		f.Fatal(err)
	}
	empty, err := encode(&cluster.Map{})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add(empty)
	f.Add(valid[:headerLen])
	f.Add(valid[:len(valid)-1])
	f.Add([]byte(magic))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzDecodeOne(t, data)
	})
}

// FuzzDecodePayload wraps arbitrary bytes in a header whose checksum is
// correct, so the fuzzer reaches the protobuf decoder instead of stopping at
// the checksum. The same invariants as FuzzDecode apply.
func FuzzDecodePayload(f *testing.F) {
	payload, err := mappb.Marshal(testMap())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(payload)
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01})
	f.Fuzz(func(t *testing.T, payload []byte) {
		fuzzDecodeOne(t, withHeader(magic, formatVersion, payload))
	})
}

// fuzzDecodeOne checks one input. A rejection must wrap ErrCorrupt. An
// accepted file must encode again, and the new file must decode to a map
// with the same encoding.
func fuzzDecodeOne(t *testing.T, data []byte) {
	t.Helper()
	m, err := decode(data)
	if err != nil {
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("rejection does not wrap ErrCorrupt: %v", err)
		}
		return
	}
	again, err := encode(m)
	if err != nil {
		t.Fatalf("accepted map does not encode: %v", err)
	}
	m2, err := decode(again)
	if err != nil {
		t.Fatalf("re-encoded map does not decode: %v", err)
	}
	a, err := mappb.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	b, err := mappb.Marshal(m2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("accepted map changes on a round trip")
	}
}

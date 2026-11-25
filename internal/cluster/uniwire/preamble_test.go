package uniwire

import (
	"bytes"
	"testing"
)

func TestPreambleRoundTrip(t *testing.T) {
	for _, kind := range []byte{kindRPC, kindStream} {
		var buf bytes.Buffer
		if err := writePreamble(&buf, kind); err != nil {
			t.Fatal(err)
		}
		if got := buf.Bytes(); len(got) != preambleLen || string(got[:4]) != "UNIW" || got[4] != 1 || got[5] != kind {
			t.Fatalf("preamble bytes = %x", got)
		}
		k, err := readPreamble(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if k != kind {
			t.Fatalf("kind = %d, want %d", k, kind)
		}
	}
}

func TestReadPreambleRejects(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{name: "empty", in: nil},
		{name: "truncated", in: []byte("UNI")},
		{name: "bad magic", in: []byte("HTTP\x01\x01")},
		{name: "future version", in: []byte("UNIW\x02\x01")},
		{name: "version zero", in: []byte("UNIW\x00\x01")},
		{name: "raft kind is not a connection kind", in: []byte("UNIW\x01\x03")},
		{name: "kind zero", in: []byte("UNIW\x01\x00")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readPreamble(bytes.NewReader(tc.in)); err == nil {
				t.Fatal("invalid preamble accepted")
			}
		})
	}
}

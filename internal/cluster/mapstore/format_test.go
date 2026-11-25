package mapstore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/mappb"
)

// withHeader returns a file with the given magic, version and a checksum
// that is correct for payload. Tests use it to reach the checks after the
// checksum.
func withHeader(mg string, version byte, payload []byte) []byte {
	b := append([]byte(mg), version)
	b = binary.BigEndian.AppendUint32(b, crc32.Checksum(payload, castagnoli))
	return append(b, payload...)
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		m    *cluster.Map
	}{
		{name: "empty", m: &cluster.Map{}},
		{name: "single node", m: &cluster.Map{Version: 1, ClusterID: "c", Nodes: []cluster.NodeInfo{{ID: "solo"}}}},
		{name: "populated", m: testMap()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := encodedMap(t, tc.m)
			got, err := decode(b)
			if err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(tc.m, got, cmpopts.EquateEmpty()); d != "" {
				t.Fatalf("round trip differs:\n%s", d)
			}
			if again := encodedMap(t, got); !bytes.Equal(again, b) {
				t.Fatal("re-encoding a decoded map changes the file")
			}
		})
	}
}

func TestEncodeLayout(t *testing.T) {
	m := testMap()
	b := encodedMap(t, m)
	payload, err := mappb.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:len(magic)]) != "UNIOCMAP" {
		t.Fatalf("magic = %q", b[:len(magic)])
	}
	if b[len(magic)] != formatVersion {
		t.Fatalf("version byte = %d, want %d", b[len(magic)], formatVersion)
	}
	if got := binary.BigEndian.Uint32(b[len(magic)+1 : headerLen]); got != crc32.Checksum(payload, castagnoli) {
		t.Fatalf("checksum field %08x is not the CRC-32C of the payload", got)
	}
	if !bytes.Equal(b[headerLen:], payload) {
		t.Fatal("payload is not mappb.Marshal of the map")
	}
}

func TestDecodeRejects(t *testing.T) {
	valid := encodedMap(t, testMap())
	payload := valid[headerLen:]
	badPayload := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	cases := []struct {
		name string
		b    []byte
		want string
	}{
		{name: "empty file", b: nil, want: "shorter than"},
		{name: "header cut short", b: valid[:headerLen-1], want: "shorter than"},
		{name: "wrong magic", b: withHeader("UNIOXMAP", formatVersion, payload), want: "bad magic"},
		{name: "unknown version", b: withHeader(magic, 2, payload), want: "format version 2 is not supported"},
		{name: "version zero", b: withHeader(magic, 0, payload), want: "format version 0"},
		{name: "bad checksum", b: func() []byte {
			b := append([]byte(nil), valid...)
			b[headerLen-1] ^= 0x01
			return b
		}(), want: "checksum mismatch"},
		{name: "payload does not decode", b: withHeader(magic, formatVersion, badPayload), want: "decode payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := decode(tc.b)
			if err == nil {
				t.Fatalf("accepted the file: %+v", m)
			}
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("error %v does not wrap ErrCorrupt", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestDecodeDetectsEveryByteFlip flips each byte of a valid file in turn. The
// magic, the version, the checksum and the payload are all covered, so every
// flip must be reported as corruption.
func TestDecodeDetectsEveryByteFlip(t *testing.T) {
	valid := encodedMap(t, testMap())
	for i := range valid {
		for _, mask := range []byte{0x01, 0x80, 0xff} {
			b := append([]byte(nil), valid...)
			b[i] ^= mask
			if _, err := decode(b); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("flipping byte %d with mask %#x: error %v, want ErrCorrupt", i, mask, err)
			}
		}
	}
}

// TestDecodeDetectsEveryTruncation cuts a valid file at every length and
// appends a byte to it. Both must be reported as corruption.
func TestDecodeDetectsEveryTruncation(t *testing.T) {
	valid := encodedMap(t, testMap())
	for n := range len(valid) {
		if _, err := decode(valid[:n]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("file cut to %d of %d bytes: error %v, want ErrCorrupt", n, len(valid), err)
		}
	}
	extra := append(append([]byte(nil), valid...), 0)
	if _, err := decode(extra); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("file with a trailing byte: error %v, want ErrCorrupt", err)
	}
}

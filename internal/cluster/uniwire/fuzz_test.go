package uniwire

import (
	"bytes"
	"encoding/binary"
	"testing"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// fuzzMaxFrame keeps fuzzed frames small so that the fuzzer explores the
// decoders rather than large allocations.
const fuzzMaxFrame = 64 << 10

func FuzzReadPreamble(f *testing.F) {
	f.Add([]byte("UNIW\x01\x01"))
	f.Add([]byte("UNIW\x01\x02"))
	f.Add([]byte("UNIW\x01\x09"))
	f.Add([]byte("XXXX\x01\x01"))
	f.Fuzz(func(t *testing.T, data []byte) {
		kind, err := readPreamble(bytes.NewReader(data))
		if err == nil && kind != kindRPC && kind != kindStream {
			t.Fatalf("accepted unknown kind %d", kind)
		}
	})
}

func FuzzReadRPCFrame(f *testing.F) {
	seed, err := appendRPCFrame(nil, fuzzMaxFrame, frameRequest, 7, "meta.Apply", []byte("payload"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	ping, _ := appendRPCFrame(nil, fuzzMaxFrame, framePing, 0, "")
	f.Add(ping)
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, err := readRPCFrame(bytes.NewReader(data), fuzzMaxFrame)
		if err != nil {
			return
		}
		// Every accepted frame must re-encode to exactly the bytes it was
		// read from: the decoder accepts no alternative encodings.
		n := int(binary.BigEndian.Uint32(data[:4]))
		again, err := appendRPCFrame(nil, fuzzMaxFrame, fr.typ, fr.id, fr.method, fr.payload)
		if err != nil {
			t.Fatalf("accepted frame does not re-encode: %v", err)
		}
		if !bytes.Equal(again, data[:4+n]) {
			t.Fatalf("re-encoding differs from input")
		}
	})
}

func FuzzReadMessageFrame(f *testing.F) {
	seed, err := appendMessageFrame(nil, fuzzMaxFrame, &uniov1.StreamHeader{Id: 1, Method: "drive.ReadShard", Length: 10})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{0, 0, 0, 2, 0xff, 0xff})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		var h uniov1.StreamHeader
		_ = readMessageFrame(bytes.NewReader(data), fuzzMaxFrame, &h)
	})
}

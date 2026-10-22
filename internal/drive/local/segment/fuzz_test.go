package segment

import (
	"bytes"
	"testing"
)

// FuzzDecodeHeader checks that arbitrary bytes never panic the decoder and
// that every accepted header re-encodes to exactly the bytes it came from.
func FuzzDecodeHeader(f *testing.F) {
	f.Add(Header{ShardID: testID(1), Length: 12, DataCRC: 7}.AppendHeader(nil))
	f.Add(Header{Flags: FlagTombstone, ShardID: testID(2), Length: 8}.AppendHeader(nil))
	f.Add([]byte("UNSR"))
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := DecodeHeader(b)
		if err != nil {
			return
		}
		enc := h.AppendHeader(nil)
		if !bytes.Equal(enc, b[:HeaderSize]) {
			t.Fatalf("accepted header does not round-trip: %x vs %x", enc, b[:HeaderSize])
		}
	})
}

// FuzzScan feeds arbitrary segment bytes to the scanner. Scan must not panic,
// must report an end inside the input, and every record it yields with data
// verification must carry matching checksums.
func FuzzScan(f *testing.F) {
	valid := AppendRecord(nil, 0, testID(3), []byte("seed payload"))
	valid = AppendRecord(valid, FlagTombstone, testID(3), TombstoneOrigin(9))
	f.Add(valid)
	f.Add(valid[:len(valid)-3])
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, verify := range []bool{false, true} {
			end, err := Scan(bytes.NewReader(b), int64(len(b)), verify, func(r Record) error {
				if verify && Checksum(r.Data) != r.Header.DataCRC {
					t.Fatalf("scan yielded a record with a bad checksum")
				}
				return nil
			})
			if err != nil {
				t.Fatalf("scan of in-memory bytes failed: %v", err)
			}
			if end < 0 || end > int64(len(b)) {
				t.Fatalf("end %d outside input of %d bytes", end, len(b))
			}
		}
	})
}

package segment

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func testID(i uint16) ids.ShardID {
	return ids.ShardID{Data: ids.DataID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, Index: i}
}

func TestHeaderRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		h    Header
	}{
		{"live", Header{ShardID: testID(0), Length: 0, DataCRC: 0}},
		{"live-data", Header{ShardID: testID(7), Length: 4096, DataCRC: 0xdeadbeef}},
		{"tombstone", Header{Flags: FlagTombstone, ShardID: testID(65535), Length: 8, DataCRC: 1}},
		{"max-length", Header{ShardID: testID(1), Length: MaxDataLength, DataCRC: 42}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := tc.h.AppendHeader(nil)
			if len(enc) != HeaderSize {
				t.Fatalf("encoded %d bytes, want %d", len(enc), HeaderSize)
			}
			got, err := DecodeHeader(enc)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got != tc.h {
				t.Fatalf("got %+v, want %+v", got, tc.h)
			}
			if again := got.AppendHeader(nil); !bytes.Equal(again, enc) {
				t.Fatalf("re-encoding differs")
			}
		})
	}
}

func TestDecodeHeaderRejects(t *testing.T) {
	good := Header{ShardID: testID(3), Length: 10, DataCRC: 99}.AppendHeader(nil)
	mutate := func(f func(b []byte)) []byte {
		b := append([]byte(nil), good...)
		f(b)
		return b
	}
	cases := []struct {
		name string
		in   []byte
	}{
		{"short", good[:HeaderSize-1]},
		{"empty", nil},
		{"bad-magic", mutate(func(b []byte) { b[0] = 'X' })},
		{"bad-version", mutate(func(b []byte) { b[4] = 2 })},
		{"unknown-flag", mutate(func(b []byte) { b[5] = 0x80 })},
		{"reserved", mutate(func(b []byte) { b[6] = 1 })},
		{"header-crc", mutate(func(b []byte) { b[9] ^= 0xff })},
		{"data-crc-field-flipped", mutate(func(b []byte) { b[35] ^= 1 })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeHeader(tc.in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestDecodeHeaderRejectsOversizedLength(t *testing.T) {
	// Re-encode with a length above MaxDataLength but a valid header CRC, so
	// only the length check can reject it.
	h := Header{ShardID: testID(1), Length: MaxDataLength + 1}
	if _, err := DecodeHeader(h.AppendHeader(nil)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestAppendRecordLayout(t *testing.T) {
	data := []byte("hello shard")
	rec := AppendRecord(nil, 0, testID(9), data)
	if int64(len(rec)) != RecordSize(int64(len(data))) {
		t.Fatalf("record is %d bytes", len(rec))
	}
	h, err := DecodeHeader(rec)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if h.Length != uint64(len(data)) || h.DataCRC != Checksum(data) || h.ShardID != testID(9) {
		t.Fatalf("header %+v does not describe data", h)
	}
	if !bytes.Equal(rec[HeaderSize:], data) {
		t.Fatal("data not stored after header")
	}
}

func TestTombstoneOrigin(t *testing.T) {
	for _, origin := range []uint64{0, 1, 1<<63 + 5} {
		got, err := ParseTombstoneOrigin(TombstoneOrigin(origin))
		if err != nil || got != origin {
			t.Fatalf("origin %d: got %d, %v", origin, got, err)
		}
	}
	if _, err := ParseTombstoneOrigin([]byte{1, 2, 3}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short payload: err = %v", err)
	}
}

package logstore

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// assertSameLog compares every field of a raft.Log. Data and Extensions must
// match in nil-ness as well as content, and AppendedAt must name the same
// instant (location and monotonic reading are not part of the encoding).
func assertSameLog(t *testing.T, got, want raft.Log) {
	t.Helper()
	if got.Index != want.Index || got.Term != want.Term || got.Type != want.Type {
		t.Fatalf("header: got index %d term %d type %v, want index %d term %d type %v",
			got.Index, got.Term, got.Type, want.Index, want.Term, want.Type)
	}
	if (got.Data == nil) != (want.Data == nil) || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("Data: got %#v, want %#v", got.Data, want.Data)
	}
	if (got.Extensions == nil) != (want.Extensions == nil) || !bytes.Equal(got.Extensions, want.Extensions) {
		t.Fatalf("Extensions: got %#v, want %#v", got.Extensions, want.Extensions)
	}
	if got.AppendedAt.IsZero() != want.AppendedAt.IsZero() || !got.AppendedAt.Equal(want.AppendedAt) {
		t.Fatalf("AppendedAt: got %v, want %v", got.AppendedAt, want.AppendedAt)
	}
}

// TestEncodeDecodeRoundTrip checks that every field survives encoding,
// including the nil versus empty distinction of Data and Extensions.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	utc := time.Date(2026, 10, 22, 12, 0, 0, 987654321, time.UTC)
	preEpoch := time.Date(1931, 3, 4, 5, 6, 7, 1, time.UTC)
	local := time.Date(2026, 1, 2, 3, 4, 5, 6, time.Local)
	slices := map[string][]byte{"nil": nil, "empty": {}, "bytes": []byte("payload")}
	times := map[string]time.Time{"zero": {}, "utc": utc, "pre-epoch": preEpoch, "local": local}

	for dataName, data := range slices {
		for extName, ext := range slices {
			for timeName, at := range times {
				want := raft.Log{
					Index: 42, Term: 7, Type: raft.LogConfiguration,
					Data: data, Extensions: ext, AppendedAt: at,
				}
				name := "data=" + dataName + "/ext=" + extName + "/at=" + timeName
				t.Run(name, func(t *testing.T) {
					enc, err := encodeLog(&want)
					if err != nil {
						t.Fatalf("encodeLog: %v", err)
					}
					got, err := decodeLog(enc)
					if err != nil {
						t.Fatalf("decodeLog: %v", err)
					}
					assertSameLog(t, got, want)
				})
			}
		}
	}
}

// TestDecodedLogDoesNotAliasInput checks that decodeLog copies its fields,
// because Pebble reuses the buffer it hands to GetLog's caller.
func TestDecodedLogDoesNotAliasInput(t *testing.T) {
	enc, err := encodeLog(&raft.Log{Index: 1, Data: []byte("abc"), Extensions: []byte("xy")})
	if err != nil {
		t.Fatalf("encodeLog: %v", err)
	}
	got, err := decodeLog(enc)
	if err != nil {
		t.Fatalf("decodeLog: %v", err)
	}
	for i := range enc {
		enc[i] = 0xEE
	}
	if string(got.Data) != "abc" || string(got.Extensions) != "xy" {
		t.Fatalf("decoded fields alias the input: data %q, extensions %q", got.Data, got.Extensions)
	}
}

// TestDecodeRejectsCorruptInput checks that damaged encodings return an
// error and never panic.
func TestDecodeRejectsCorruptInput(t *testing.T) {
	const (
		offFlags   = 18
		offNsec    = 27
		offDataLen = 31
	)
	valid, err := encodeLog(&raft.Log{Index: 9, Term: 3, Data: []byte("abc"), Extensions: []byte("xy")})
	if err != nil {
		t.Fatalf("encodeLog: %v", err)
	}
	if len(valid) != offDataLen+4+3+4+2 {
		t.Fatalf("encoding is %d bytes, layout expects %d", len(valid), offDataLen+4+3+4+2)
	}

	mutate := func(f func(b []byte) []byte) []byte {
		b := bytes.Clone(valid)
		return f(b)
	}
	cases := []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"trailing byte", append(bytes.Clone(valid), 0)},
		{"unsupported version", mutate(func(b []byte) []byte { b[0] = 2; return b })},
		{"unknown flag bit", mutate(func(b []byte) []byte { b[offFlags] |= 0x80; return b })},
		{"nanoseconds out of range", mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[offNsec:], 1_000_000_000)
			return b
		})},
		{"data length beyond input", mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[offDataLen:], 16)
			return b
		})},
		{"data length 2^32-1", mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[offDataLen:], ^uint32(0))
			return b
		})},
		{"nil flag with data bytes", mutate(func(b []byte) []byte { b[offFlags] |= flagDataNil; return b })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeLog(tc.in); err == nil {
				t.Fatal("decodeLog accepted corrupt input")
			}
		})
	}

	t.Run("every proper prefix", func(t *testing.T) {
		for n := range valid {
			if _, err := decodeLog(valid[:n]); err == nil {
				t.Fatalf("decodeLog accepted a %d byte prefix of a %d byte encoding", n, len(valid))
			}
		}
	})

	t.Run("every single byte overwrite", func(t *testing.T) {
		// Results are ignored: a changed payload byte is still a valid log.
		// The check is that no position and no value makes decoding panic.
		for i := range valid {
			for _, v := range []byte{0x00, 0x7f, 0xff} {
				b := bytes.Clone(valid)
				b[i] = v
				_, _ = decodeLog(b)
			}
		}
	})
}

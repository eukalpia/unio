package payload

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"hash/crc32"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// FuzzBody checks the object.Body invariants for arbitrary payloads, chunk
// sizes and declared lengths that are off by a small delta.
func FuzzBody(f *testing.F) {
	f.Add([]byte(checkInput), []byte{1, 2, 3}, int8(0))
	f.Add([]byte{}, []byte{0}, int8(0))
	f.Add([]byte{}, []byte{}, int8(1))
	f.Add([]byte("abc"), []byte{}, int8(-1))
	f.Add([]byte("abc"), []byte{7, 200}, int8(2))
	f.Add(bytes.Repeat([]byte{0xa5}, 1000), []byte{255, 3, 17, 1}, int8(0))

	f.Fuzz(func(t *testing.T, payload, sizes []byte, delta int8) {
		declared := int64(len(payload)) + int64(delta)
		if declared < 0 {
			if _, err := New(bytes.NewReader(payload), Spec{Size: declared}); err == nil {
				t.Fatal("negative declared length accepted")
			}
			return
		}
		md5sum := md5.Sum(payload)
		shasum := sha256.Sum256(payload)
		crc := be32(crc32.Checksum(payload, crc32.MakeTable(crc32.Castagnoli)))
		src := &fuzzReader{data: payload, sizes: sizes, combine: len(sizes) > 0 && sizes[0]%2 == 1}
		b, err := New(src, Spec{
			Size:       declared,
			ContentMD5: md5sum[:],
			SHA256:     shasum[:],
			Algorithm:  object.ChecksumCRC32C,
			Expected:   crc,
		})
		if err != nil {
			t.Fatal(err)
		}
		got, err := readFuzzed(t, b, sizes, len(payload))
		switch {
		case delta == 0:
			if err != nil {
				t.Fatalf("well-formed payload rejected: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("io.EOF after %d bytes that differ from the %d-byte payload", len(got), len(payload))
			}
			if b.Size() != int64(len(got)) {
				t.Fatalf("declared size %d, delivered %d bytes", b.Size(), len(got))
			}
			if b.MD5() != md5sum {
				t.Fatal("MD5 after io.EOF differs from the payload digest")
			}
			if c := b.Checksum(); !bytes.Equal(c.Value, crc) {
				t.Fatalf("checksum after io.EOF = %x, want %x", c.Value, crc)
			}
		case delta < 0:
			requireCode(t, err, s3err.InvalidRequest)
		default:
			requireCode(t, err, s3err.IncompleteBody)
		}
	})
}

// fuzzReader serves data in chunks whose lengths come from sizes. When combine
// is set, it returns io.EOF together with the last chunk.
type fuzzReader struct {
	data    []byte
	sizes   []byte
	combine bool
	calls   int
}

func (r *fuzzReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	chunk := 1
	if len(r.sizes) > 0 {
		chunk += int(r.sizes[r.calls%len(r.sizes)])
	}
	r.calls++
	n := copy(p, r.data[:min(chunk, len(r.data))])
	r.data = r.data[n:]
	if len(r.data) == 0 && r.combine {
		return n, io.EOF
	}
	return n, nil
}

// readFuzzed reads b with buffer lengths taken from sizes and returns the
// delivered bytes and the terminal error, with io.EOF reported as nil. Every
// call either delivers a byte or reaches a terminal result, so the loop bound
// only guards against a stalled Body.
func readFuzzed(t *testing.T, b *Body, sizes []byte, payloadLen int) ([]byte, error) {
	t.Helper()
	const maxBuf = 512
	buf := make([]byte, maxBuf)
	var got []byte
	for i := range payloadLen + 8 {
		size := 1
		if len(sizes) > 0 {
			size = 1 + int(sizes[i%len(sizes)])%(maxBuf-1)
		}
		n, err := b.Read(buf[:size])
		got = append(got, buf[:n]...)
		if err == io.EOF {
			return got, nil
		}
		if err != nil {
			return got, err
		}
	}
	t.Fatal("body stalled without a terminal result")
	return nil, nil
}

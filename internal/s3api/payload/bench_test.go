package payload

import (
	"bytes"
	"crypto/sha256"
	"hash/crc32"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/minio/crc64nvme"
)

const (
	benchPayloadSize = 1 << 20
	benchReadSize    = 32 << 10
)

// benchPayload returns a deterministic 1 MiB payload.
func benchPayload() []byte {
	p := make([]byte, benchPayloadSize)
	for i := range p {
		p[i] = byte(i*131 + i>>7)
	}
	return p
}

// benchRead builds a Body over payload with spec and drains it in
// benchReadSize chunks on every iteration.
func benchRead(b *testing.B, payload []byte, spec Spec) {
	src := bytes.NewReader(payload)
	buf := make([]byte, benchReadSize)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		src.Reset(payload)
		body, err := New(src, spec)
		if err != nil {
			b.Fatal(err)
		}
		for {
			_, err := body.Read(buf)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkBodyMD5 measures the MD5 that every Body computes, with no
// additional checksum requested.
func BenchmarkBodyMD5(b *testing.B) {
	payload := benchPayload()
	benchRead(b, payload, Spec{Size: int64(len(payload))})
}

// BenchmarkBodyAll measures MD5, SHA-256 and CRC32C together.
func BenchmarkBodyAll(b *testing.B) {
	payload := benchPayload()
	shasum := sha256.Sum256(payload)
	crc := be32(crc32.Checksum(payload, crc32.MakeTable(crc32.Castagnoli)))
	benchRead(b, payload, Spec{
		Size:      int64(len(payload)),
		SHA256:    shasum[:],
		Algorithm: object.ChecksumCRC32C,
		Expected:  crc,
	})
}

// BenchmarkBodyCRC64NVME measures MD5 together with CRC64NVME.
func BenchmarkBodyCRC64NVME(b *testing.B) {
	payload := benchPayload()
	h := crc64nvme.New()
	_, _ = h.Write(payload)
	benchRead(b, payload, Spec{
		Size:      int64(len(payload)),
		Algorithm: object.ChecksumCRC64NVME,
		Expected:  h.Sum(nil),
	})
}

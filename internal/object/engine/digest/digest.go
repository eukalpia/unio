// Package digest creates hash functions for the S3 additional checksum
// algorithms (CRC32, CRC32C, CRC64NVME, SHA-1, SHA-256) on top of the
// standard library. Full-object and composite checksums are both built from
// these hashes.
package digest

import (
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"hash"
	"hash/crc32"
	"hash/crc64"

	"github.com/eukalpia/unio/internal/object"
)

// crc64NVMEPoly is the bit-reversed form of the CRC-64/NVME polynomial
// 0xAD93D23594C93659, the representation hash/crc64 expects.
const crc64NVMEPoly = 0x9a6c9329ac4bc9b5

// The tables are immutable once built; they are shared by all callers.
var (
	castagnoliTable = crc32.MakeTable(crc32.Castagnoli)
	nvmeTable       = crc64.MakeTable(crc64NVMEPoly)
)

// New returns a fresh hash for alg. The digest of a hash is the raw
// big-endian checksum value that the S3 headers carry (base64 encoded).
// It fails for object.ChecksumNone and unknown algorithms.
func New(alg object.ChecksumAlgorithm) (hash.Hash, error) {
	switch alg {
	case object.ChecksumCRC32:
		return crc32.NewIEEE(), nil
	case object.ChecksumCRC32C:
		return crc32.New(castagnoliTable), nil
	case object.ChecksumCRC64NVME:
		return crc64.New(nvmeTable), nil
	case object.ChecksumSHA1:
		return sha1.New(), nil
	case object.ChecksumSHA256:
		return sha256.New(), nil
	}
	return nil, fmt.Errorf("digest: unsupported checksum algorithm %d", alg)
}

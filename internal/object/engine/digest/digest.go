// Package digest creates hash functions for the S3 additional checksum
// algorithms (CRC32, CRC32C, CRC64NVME, SHA-1, SHA-256) on top of the
// standard library. Full-object and composite checksums are both built from
// these hashes.
//
// MD5 and SHA-1 are part of the S3 protocol: ETags are MD5 digests and the
// SHA-1 checksum is one of the algorithms clients may request. They identify
// content and are never used for an authentication or integrity decision
// inside Unio. Those decisions use SigV4 and the bitrot hashes of the storage
// layer.
package digest

import (
	"crypto/md5"  // #nosec G501 -- S3 ETags are MD5 digests by protocol, not a security check
	"crypto/sha1" // #nosec G505 -- SHA-1 is an S3 checksum algorithm that clients request
	"crypto/sha256"
	"fmt"
	"hash"
	"hash/crc32"
	"hash/crc64"

	"github.com/eukalpia/unio/internal/object"
)

// MD5Size is the length of an MD5 digest in bytes.
const MD5Size = md5.Size

// crc64NVMEPoly is the bit-reversed form of the CRC-64/NVME polynomial
// 0xAD93D23594C93659, the representation hash/crc64 expects.
const crc64NVMEPoly = 0x9a6c9329ac4bc9b5

// The tables are immutable once built; they are shared by all callers.
var (
	castagnoliTable = crc32.MakeTable(crc32.Castagnoli)
	nvmeTable       = crc64.MakeTable(crc64NVMEPoly)
)

// NewMD5 returns a fresh MD5 hash, the digest of S3 ETags.
func NewMD5() hash.Hash {
	return md5.New() // #nosec G401 -- S3 ETag digest, see the package comment
}

// SumMD5 returns the MD5 digest of data, the ETag of single-part content.
func SumMD5(data []byte) [MD5Size]byte {
	return md5.Sum(data) // #nosec G401 -- S3 ETag digest, see the package comment
}

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
		return sha1.New(), nil // #nosec G401 -- requested S3 checksum algorithm, see the package comment
	case object.ChecksumSHA256:
		return sha256.New(), nil
	}
	return nil, fmt.Errorf("digest: unsupported checksum algorithm %d", alg)
}

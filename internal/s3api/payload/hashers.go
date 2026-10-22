package payload

import (
	"crypto/sha1" // #nosec G505 -- SHA-1 is required by the S3 protocol (x-amz-checksum-sha1); not used for security
	"crypto/sha256"
	"hash"
	"hash/crc32"

	"github.com/eukalpia/unio/internal/object"
	"github.com/minio/crc64nvme"
)

// digestLen is the digest length in bytes of each checksum algorithm, indexed
// by object.ChecksumAlgorithm. It is read-only.
var digestLen = [...]int{
	object.ChecksumNone:      0,
	object.ChecksumCRC32:     4,
	object.ChecksumCRC32C:    4,
	object.ChecksumCRC64NVME: 8,
	object.ChecksumSHA1:      20,
	object.ChecksumSHA256:    32,
}

// digestLength returns the digest length of a. The boolean is false when a is
// not a known algorithm.
func digestLength(a object.ChecksumAlgorithm) (int, bool) {
	if int(a) >= len(digestLen) {
		return 0, false
	}
	return digestLen[a], true
}

// newHash returns a fresh hasher for a. It returns nil for ChecksumNone and for
// unknown algorithms.
func newHash(a object.ChecksumAlgorithm) hash.Hash {
	switch a {
	case object.ChecksumCRC32:
		return crc32.NewIEEE()
	case object.ChecksumCRC32C:
		return crc32.New(crc32.MakeTable(crc32.Castagnoli))
	case object.ChecksumCRC64NVME:
		return crc64nvme.New()
	case object.ChecksumSHA1:
		return sha1.New() // #nosec G401 -- SHA-1 is required by the S3 protocol (x-amz-checksum-sha1); not used for security
	case object.ChecksumSHA256:
		return sha256.New()
	default:
		return nil
	}
}

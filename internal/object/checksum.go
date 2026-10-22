package object

import (
	"encoding/base64"
	"strconv"
)

// ChecksumAlgorithm is an S3 additional checksum algorithm.
type ChecksumAlgorithm uint8

// Supported checksum algorithms.
const (
	ChecksumNone ChecksumAlgorithm = iota
	ChecksumCRC32
	ChecksumCRC32C
	ChecksumCRC64NVME
	ChecksumSHA1
	ChecksumSHA256
)

var checksumNames = [...]string{"", "CRC32", "CRC32C", "CRC64NVME", "SHA1", "SHA256"}

// String returns the S3 name of the algorithm ("CRC32C", ...), or "" for none.
func (a ChecksumAlgorithm) String() string {
	if int(a) < len(checksumNames) {
		return checksumNames[a]
	}
	return ""
}

// ParseChecksumAlgorithm parses an S3 algorithm name, case-sensitively as
// S3 does. It returns false for unknown names.
func ParseChecksumAlgorithm(s string) (ChecksumAlgorithm, bool) {
	for i, n := range checksumNames {
		if i > 0 && n == s {
			return ChecksumAlgorithm(i), true
		}
	}
	return ChecksumNone, false
}

// ChecksumType distinguishes full-object checksums from composite
// (checksum-of-checksums) checksums of multipart objects.
type ChecksumType uint8

// Checksum types.
const (
	ChecksumFullObject ChecksumType = iota
	ChecksumComposite
)

// Checksum is an S3 additional checksum value.
type Checksum struct {
	Algorithm ChecksumAlgorithm
	Type      ChecksumType
	// Value is the raw digest.
	Value []byte
	// Parts is the number of parts for composite checksums, else 0.
	Parts int
}

// IsZero reports whether no checksum is present.
func (c Checksum) IsZero() bool { return c.Algorithm == ChecksumNone }

// Base64 returns the S3 header representation of the checksum value,
// with the "-<parts>" suffix for composite checksums.
func (c Checksum) Base64() string {
	s := base64.StdEncoding.EncodeToString(c.Value)
	if c.Type == ChecksumComposite && c.Parts > 0 {
		s += "-" + strconv.Itoa(c.Parts)
	}
	return s
}

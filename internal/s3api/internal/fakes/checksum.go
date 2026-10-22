package fakes

import (
	"crypto/sha1" // #nosec G505 -- SHA-1 is required by the S3 protocol (x-amz-checksum-sha1); not used for security
	"crypto/sha256"
	"hash"
	"hash/crc32"
	"hash/crc64"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// crc64NVMEPoly is the bit-reversed form of the CRC-64/NVME polynomial
// 0xAD93D23594C93659, the input that hash/crc64 expects. hash/crc64 applies
// the same initial and final inversion as CRC-64/NVME.
const crc64NVMEPoly = 0x9a6c9329ac4bc9b5

// newHash returns a fresh hash for alg. Its Sum output is the big-endian
// digest that S3 base64-encodes. Unknown algorithms are InvalidArgument.
func newHash(alg object.ChecksumAlgorithm) (hash.Hash, error) {
	switch alg {
	case object.ChecksumCRC32:
		return crc32.NewIEEE(), nil
	case object.ChecksumCRC32C:
		return crc32.New(crc32.MakeTable(crc32.Castagnoli)), nil
	case object.ChecksumCRC64NVME:
		return crc64.New(crc64.MakeTable(crc64NVMEPoly)), nil
	case object.ChecksumSHA1:
		return sha1.New(), nil // #nosec G401 -- SHA-1 is required by the S3 protocol (x-amz-checksum-sha1); not used for security
	case object.ChecksumSHA256:
		return sha256.New(), nil
	}
	return nil, s3err.Errorf(s3err.InvalidArgument, "unsupported checksum algorithm %d", uint8(alg))
}

// checkAlgorithm reports an error when alg is neither ChecksumNone nor a
// supported algorithm.
func checkAlgorithm(alg object.ChecksumAlgorithm) error {
	if alg == object.ChecksumNone {
		return nil
	}
	_, err := newHash(alg)
	return err
}

// checksumOf returns the full-object checksum of data for alg, or the zero
// Checksum when alg is ChecksumNone.
func checksumOf(alg object.ChecksumAlgorithm, data []byte) (object.Checksum, error) {
	if alg == object.ChecksumNone {
		return object.Checksum{}, nil
	}
	h, err := newHash(alg)
	if err != nil {
		return object.Checksum{}, err
	}
	_, _ = h.Write(data)
	return object.Checksum{Algorithm: alg, Type: object.ChecksumFullObject, Value: h.Sum(nil)}, nil
}

// compositeChecksum returns the composite checksum of a multipart object
// whose parts have the given data: the checksum of the concatenated
// full-object checksums of the parts, with one entry per part.
func compositeChecksum(alg object.ChecksumAlgorithm, parts [][]byte) (object.Checksum, error) {
	h, err := newHash(alg)
	if err != nil {
		return object.Checksum{}, err
	}
	var digests []byte
	for _, p := range parts {
		c, err := checksumOf(alg, p)
		if err != nil {
			return object.Checksum{}, err
		}
		digests = append(digests, c.Value...)
	}
	_, _ = h.Write(digests)
	return object.Checksum{
		Algorithm: alg,
		Type:      object.ChecksumComposite,
		Value:     h.Sum(nil),
		Parts:     len(parts),
	}, nil
}

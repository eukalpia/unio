package keys

import (
	"encoding/binary"
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
)

// Part returns the key of one uploaded part of an upload:
// P | bucketID | esc(uploadID) 00 01 | partNumber(4). The part number is
// big-endian, so the parts of an upload sort by number.
func Part(bucket ids.BucketID, uploadID []byte, number uint32) []byte {
	return binary.BigEndian.AppendUint32(PartUploadPrefix(bucket, uploadID), number)
}

// PartUploadPrefix returns the prefix shared by all parts of one upload:
// P | bucketID | esc(uploadID) 00 01.
func PartUploadPrefix(bucket ids.BucketID, uploadID []byte) []byte {
	return AppendTerminated(appendBucket([]byte{PrefixPart}, bucket), uploadID)
}

// ParsePart decodes a key built by Part. The returned upload id may alias k,
// so callers that keep it must copy it. It returns an error wrapping
// ErrMalformed for any other input.
func ParsePart(k []byte) (ids.BucketID, []byte, uint32, error) {
	const kind = "part"
	body, err := checkPrefix(kind, k, PrefixPart)
	if err != nil {
		return 0, nil, 0, err
	}
	bucket, body, err := readBucket(kind, body)
	if err != nil {
		return 0, nil, 0, err
	}
	uploadID, rest, err := readTerminated(kind, body)
	if err != nil {
		return 0, nil, 0, err
	}
	if len(rest) != 4 {
		return 0, nil, 0, fmt.Errorf("%w: %s: part number needs 4 bytes, have %d", ErrMalformed, kind, len(rest))
	}
	return bucket, uploadID, binary.BigEndian.Uint32(rest), nil
}

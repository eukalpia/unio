package keys

import (
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
)

// Control returns the key of the control record of bucket: F | bucketID. The
// data partitions hold one such record per bucket.
func Control(bucket ids.BucketID) []byte {
	return appendBucket([]byte{PrefixControl}, bucket)
}

// ParseControl decodes a key built by Control. It returns an error wrapping
// ErrMalformed for any other input, including trailing bytes.
func ParseControl(k []byte) (ids.BucketID, error) {
	const kind = "control"
	body, err := checkPrefix(kind, k, PrefixControl)
	if err != nil {
		return 0, err
	}
	bucket, rest, err := readBucket(kind, body)
	if err != nil {
		return 0, err
	}
	if len(rest) != 0 {
		return 0, fmt.Errorf("%w: %s: %d trailing bytes", ErrMalformed, kind, len(rest))
	}
	return bucket, nil
}

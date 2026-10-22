package keys

import (
	"encoding/binary"
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
)

// checkPrefix verifies that k starts with prefix and returns the bytes after it.
func checkPrefix(kind string, k []byte, prefix byte) ([]byte, error) {
	if len(k) == 0 {
		return nil, fmt.Errorf("%w: %s: empty input", ErrMalformed, kind)
	}
	if k[0] != prefix {
		return nil, fmt.Errorf("%w: %s: leading byte %#x, want %#x", ErrMalformed, kind, k[0], prefix)
	}
	return k[1:], nil
}

// appendBucket appends bucket as 8 big-endian bytes.
func appendBucket(dst []byte, bucket ids.BucketID) []byte {
	return binary.BigEndian.AppendUint64(dst, uint64(bucket))
}

// readBucket decodes the 8-byte bucket id at the start of b and returns the
// bytes after it.
func readBucket(kind string, b []byte) (ids.BucketID, []byte, error) {
	if len(b) < 8 {
		return 0, nil, fmt.Errorf("%w: %s: bucket id needs 8 bytes, have %d", ErrMalformed, kind, len(b))
	}
	return ids.BucketID(binary.BigEndian.Uint64(b[:8])), b[8:], nil
}

package keys

import (
	"encoding/binary"
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
)

// ObjectVersion returns the key of one version of key in bucket:
// O | bucketID | esc(key) 00 01 | ^ts. The timestamp is bitwise inverted, so
// newer versions (larger ts) sort first. Null versions have no timestamp
// of their own; they are ordinary versions with an empty version id.
func ObjectVersion(bucket ids.BucketID, key []byte, ts uint64) []byte {
	return binary.BigEndian.AppendUint64(ObjectVersionPrefix(bucket, key), ^ts)
}

// ObjectVersionPrefix returns the prefix shared by all versions of key in
// bucket: O | bucketID | esc(key) 00 01.
func ObjectVersionPrefix(bucket ids.BucketID, key []byte) []byte {
	return AppendTerminated(ObjectBucketPrefix(bucket), key)
}

// ObjectBucketPrefix returns the prefix shared by all object keys of bucket:
// O | bucketID.
func ObjectBucketPrefix(bucket ids.BucketID) []byte {
	return appendBucket([]byte{PrefixObject}, bucket)
}

// ObjectKeyStart returns the lower bound of the keys whose names start with
// prefix in bucket: O | bucketID | esc(prefix), with no terminator. Every
// version of every such key sorts at or after this value.
func ObjectKeyStart(bucket ids.BucketID, prefix []byte) []byte {
	return AppendEscaped(ObjectBucketPrefix(bucket), prefix)
}

// ObjectKeySeekPast returns the exclusive upper bound of the keys whose names
// start with prefix in bucket. The keys with that prefix are exactly the
// object versions in [ObjectKeyStart, ObjectKeySeekPast). Use it as the end
// of a range scan or as a seek target that skips past the prefix.
//
// The bound is O | bucketID | Successor(esc(prefix)). If the prefix has no
// successor (it is empty or all 0xFF), the prefix covers the rest of the
// bucket, and the bound is Successor(ObjectBucketPrefix(bucket)). That is the
// end of the bucket's range. It is never nil, so an iterator cannot run past
// the bucket into other buckets by mistake.
func ObjectKeySeekPast(bucket ids.BucketID, prefix []byte) []byte {
	if s := Successor(AppendEscaped(nil, prefix)); s != nil {
		return append(ObjectBucketPrefix(bucket), s...)
	}
	return Successor(ObjectBucketPrefix(bucket))
}

// ParseObjectVersion decodes a key built by ObjectVersion. The returned key
// may alias k, so callers that keep it must copy it. It returns an error
// wrapping ErrMalformed for any other input.
func ParseObjectVersion(k []byte) (ids.BucketID, []byte, uint64, error) {
	const kind = "object version"
	body, err := checkPrefix(kind, k, PrefixObject)
	if err != nil {
		return 0, nil, 0, err
	}
	bucket, body, err := readBucket(kind, body)
	if err != nil {
		return 0, nil, 0, err
	}
	key, rest, err := readTerminated(kind, body)
	if err != nil {
		return 0, nil, 0, err
	}
	if len(rest) != 8 {
		return 0, nil, 0, fmt.Errorf("%w: %s: timestamp needs 8 bytes, have %d", ErrMalformed, kind, len(rest))
	}
	return bucket, key, ^binary.BigEndian.Uint64(rest), nil
}

package keys

import "github.com/eukalpia/unio/internal/ids"

// Upload returns the key of one in-progress multipart upload:
// U | bucketID | esc(key) 00 01 | uploadID. The upload id is raw bytes and
// forms the last component, so it needs no escaping.
func Upload(bucket ids.BucketID, key, uploadID []byte) []byte {
	return append(UploadKeyPrefix(bucket, key), uploadID...)
}

// UploadBucketPrefix returns the prefix shared by all uploads of bucket:
// U | bucketID.
func UploadBucketPrefix(bucket ids.BucketID) []byte {
	return appendBucket([]byte{PrefixUpload}, bucket)
}

// UploadKeyPrefix returns the prefix shared by all uploads of key in bucket:
// U | bucketID | esc(key) 00 01.
func UploadKeyPrefix(bucket ids.BucketID, key []byte) []byte {
	return AppendTerminated(UploadBucketPrefix(bucket), key)
}

// ParseUpload decodes a key built by Upload. The returned key and upload id
// may alias k, so callers that keep them must copy them. It returns an error
// wrapping ErrMalformed for any other input.
func ParseUpload(k []byte) (ids.BucketID, []byte, []byte, error) {
	const kind = "upload"
	body, err := checkPrefix(kind, k, PrefixUpload)
	if err != nil {
		return 0, nil, nil, err
	}
	bucket, body, err := readBucket(kind, body)
	if err != nil {
		return 0, nil, nil, err
	}
	key, uploadID, err := readTerminated(kind, body)
	if err != nil {
		return 0, nil, nil, err
	}
	return bucket, key, uploadID, nil
}

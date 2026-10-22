package keys

// Bucket returns the system-group record key of a bucket with the given name:
// B | name. The name is raw bytes and forms the last component.
func Bucket(name []byte) []byte {
	return append([]byte{PrefixBucket}, name...)
}

// BucketPrefix returns the prefix shared by all bucket records: B.
func BucketPrefix() []byte {
	return []byte{PrefixBucket}
}

// ParseBucket decodes a key built by Bucket and returns the bucket name. The
// name may alias k, so callers that keep it must copy it. It returns an error
// wrapping ErrMalformed for any other input.
func ParseBucket(k []byte) (name []byte, err error) {
	return checkPrefix("bucket", k, PrefixBucket)
}

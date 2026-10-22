package keys

// Kind prefixes. The first byte of every key names its kind.
const (
	// PrefixObject starts object version keys: O | bucketID | esc(key) 00 01 | ^ts.
	PrefixObject byte = 'O'
	// PrefixUpload starts multipart upload keys: U | bucketID | esc(key) 00 01 | uploadID.
	PrefixUpload byte = 'U'
	// PrefixPart starts multipart part keys: P | bucketID | esc(uploadID) 00 01 | partNumber.
	PrefixPart byte = 'P'
	// PrefixBucket starts bucket records in the system group: B | name.
	PrefixBucket byte = 'B'
	// PrefixKV starts system key-value entries in the system group: K | key.
	PrefixKV byte = 'K'
	// PrefixControl starts per-bucket control records in data partitions: F | bucketID.
	PrefixControl byte = 'F'
	// PrefixMeta starts partition metadata records: M | name.
	PrefixMeta byte = 'M'
)

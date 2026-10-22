package keys

// KV returns the system-group key of a KV entry: K | key. The key is raw
// bytes and forms the last component.
func KV(key []byte) []byte {
	return append([]byte{PrefixKV}, key...)
}

// KVPrefix returns the prefix shared by all system KV entries: K.
func KVPrefix() []byte {
	return []byte{PrefixKV}
}

// ParseKV decodes a key built by KV and returns the KV key. The key may alias
// k, so callers that keep it must copy it. It returns an error wrapping
// ErrMalformed for any other input.
func ParseKV(k []byte) (key []byte, err error) {
	return checkPrefix("kv", k, PrefixKV)
}

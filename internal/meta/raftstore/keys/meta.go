package keys

// Meta returns the key of a partition metadata record with the given name,
// such as a counter: M | name. The name is the last component.
func Meta(name string) []byte {
	return append([]byte{PrefixMeta}, name...)
}

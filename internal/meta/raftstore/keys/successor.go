package keys

// Successor returns the smallest byte string that is greater than b and
// greater than every string that has b as a prefix. It finds the last byte of
// b that is not 0xFF, drops the 0xFF bytes after it, and increments that
// byte. The result is a new slice, and b is not modified.
//
// Successor returns nil when no such string exists. That happens exactly when
// b is empty or consists only of 0xFF bytes, so nil means the end of the
// keyspace.
func Successor(b []byte) []byte {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == 0xff {
			continue
		}
		s := make([]byte, i+1)
		copy(s, b[:i+1])
		s[i]++
		return s
	}
	return nil
}

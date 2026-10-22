package xmlenc

// upperHex maps a nibble to its uppercase hexadecimal digit.
const upperHex = "0123456789ABCDEF"

// AppendPercent appends s to dst percent-encoded for encoding-type=url and
// returns the extended slice.
//
// Every byte except ASCII letters, digits and -._~/ is written as %XX with
// uppercase hex digits. A space becomes %20, never +. The encoding works on
// bytes, so multi-byte UTF-8 and invalid UTF-8 survive a round trip through
// url.PathUnescape exactly.
func AppendPercent(dst []byte, s string) []byte {
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			continue
		}
		dst = append(dst, s[start:i]...)
		dst = append(dst, '%', upperHex[c>>4], upperHex[c&0x0F])
		start = i + 1
	}
	return append(dst, s[start:]...)
}

// isUnreserved reports whether c is written as it is by AppendPercent.
func isUnreserved(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') ||
		c == '-' || c == '.' || c == '_' || c == '~' || c == '/'
}

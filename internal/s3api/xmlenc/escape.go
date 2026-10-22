package xmlenc

import "unicode/utf8"

// replacementChar is U+FFFD in UTF-8. It stands in for bytes and code points
// that XML 1.0 cannot carry.
const replacementChar = "�"

// AppendEscape appends s to dst as XML character data and returns the
// extended slice.
//
// The characters &, <, >, ", ' and CR are written as &amp;, &lt;, &gt;,
// &#34;, &#39; and &#13;, so parsers do not normalise line endings. Tab and LF
// are kept as they are. Characters that XML 1.0 cannot represent are replaced
// with U+FFFD: C0 controls other than tab, LF and CR, U+FFFE, U+FFFF,
// surrogates, and each byte of an invalid UTF-8 sequence. XML 1.0 has no
// encoding for these, so callers that need the exact bytes of a key must use
// encoding-type=url together with AppendPercent.
//
// A string with nothing to escape is copied with a single append.
func AppendEscape(dst []byte, s string) []byte {
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			rep := asciiReplacement(c)
			if rep == "" {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			dst = append(dst, rep...)
			i++
			start = i
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		// A multi-byte sequence never has size 1, so size 1 marks an invalid byte.
		if n == 1 || !isXMLChar(r) {
			dst = append(dst, s[start:i]...)
			dst = append(dst, replacementChar...)
			i += n
			start = i
			continue
		}
		i += n
	}
	return append(dst, s[start:]...)
}

// asciiReplacement returns the text that replaces the ASCII byte c, or the
// empty string when c is written as it is.
func asciiReplacement(c byte) string {
	switch c {
	case '&':
		return "&amp;"
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	case '"':
		return "&#34;"
	case '\'':
		return "&#39;"
	case '\r':
		return "&#13;"
	case '\t', '\n':
		return ""
	}
	if c < 0x20 {
		return replacementChar
	}
	return ""
}

// isXMLChar reports whether r is a character that XML 1.0 allows.
func isXMLChar(r rune) bool {
	switch {
	case r < 0x20:
		return r == '\t' || r == '\n' || r == '\r'
	case r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0x10FFFF:
		return true
	}
	return false
}

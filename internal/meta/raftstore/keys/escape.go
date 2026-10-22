package keys

import "fmt"

// Bytes of the escape and terminator codes.
const (
	nulByte       = 0x00
	escapedNUL    = 0xff // follows nulByte to encode one literal 0x00
	terminatorEnd = 0x01 // follows nulByte to end a terminated component
)

// AppendEscaped appends the escaped form of s to dst and returns the extended
// slice. Each 0x00 becomes 0x00 0xFF and other bytes are copied unchanged. No
// terminator is added.
func AppendEscaped(dst, s []byte) []byte {
	for _, c := range s {
		if c == nulByte {
			dst = append(dst, nulByte, escapedNUL)
		} else {
			dst = append(dst, c)
		}
	}
	return dst
}

// AppendTerminated appends the escaped form of s followed by the terminator
// 0x00 0x01 to dst and returns the extended slice. The terminator sorts below
// every escaped byte, so the terminated component keeps byte order.
func AppendTerminated(dst, s []byte) []byte {
	return append(AppendEscaped(dst, s), nulByte, terminatorEnd)
}

// readTerminated decodes the escaped component at the start of b and the
// terminator that ends it. It returns the unescaped component and the bytes
// after the terminator. The component aliases b when it has no escaped 0x00.
// kind names the key kind in errors.
func readTerminated(kind string, b []byte) (component, rest []byte, err error) {
	var out []byte
	copied := false // set once out holds the bytes read so far
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c != nulByte {
			if copied {
				out = append(out, c)
			}
			continue
		}
		if i+1 == len(b) {
			return nil, nil, fmt.Errorf("%w: %s: unterminated component", ErrMalformed, kind)
		}
		switch b[i+1] {
		case escapedNUL:
			if !copied {
				out = append(make([]byte, 0, len(b)), b[:i]...)
				copied = true
			}
			out = append(out, nulByte)
			i++
		case terminatorEnd:
			if !copied {
				return b[:i:i], b[i+2:], nil
			}
			return out, b[i+2:], nil
		default:
			return nil, nil, fmt.Errorf("%w: %s: invalid escape 0x00 %#x", ErrMalformed, kind, b[i+1])
		}
	}
	return nil, nil, fmt.Errorf("%w: %s: unterminated component", ErrMalformed, kind)
}

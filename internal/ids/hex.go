package ids

import (
	"encoding/hex"
	"fmt"
)

// decodeHex decodes s into dst, requiring exactly 2*len(dst) hex digits.
func decodeHex(dst []byte, s string) error {
	if len(s) != 2*len(dst) {
		return fmt.Errorf("%q: want %d hex chars", s, 2*len(dst))
	}
	if _, err := hex.Decode(dst, []byte(s)); err != nil {
		return fmt.Errorf("%q: %w", s, err)
	}
	return nil
}

package keys

import (
	"encoding/hex"
	"strings"
)

// Hex spellings of the 8-byte big-endian bucket ids used in expected keys.
const (
	bucket1 = "00 00 00 00 00 00 00 01"
	bucket2 = "00 00 00 00 00 00 00 02"
	bucket3 = "00 00 00 00 00 00 00 03"
)

// mustHex decodes a hex literal with optional spaces so that table tests can
// state exact bytes. A malformed literal is a bug in the test, so it panics.
func mustHex(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic("bad hex literal " + s + ": " + err.Error())
	}
	return b
}

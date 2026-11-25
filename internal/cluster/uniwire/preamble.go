package uniwire

import (
	"fmt"
	"io"
)

// A connection opens with a fixed preamble that selects its kind:
//
//	magic   [4]byte  "UNIW"
//	version byte     1
//	kind    byte     1 = RPC, 2 = Stream
//
// The preamble is the only thing an unauthenticated byte stream can reach
// before the TLS handshake has completed, so it is parsed strictly.
const (
	preambleMagic   = "UNIW"
	preambleVersion = 1
	preambleLen     = 6
)

// Connection kinds.
const (
	kindRPC    byte = 1
	kindStream byte = 2
)

// writePreamble writes the preamble for kind.
func writePreamble(w io.Writer, kind byte) error {
	var b [preambleLen]byte
	copy(b[:4], preambleMagic)
	b[4] = preambleVersion
	b[5] = kind
	if _, err := w.Write(b[:]); err != nil {
		return fmt.Errorf("uniwire: write preamble: %w", err)
	}
	return nil
}

// readPreamble reads and validates a preamble, returning its kind. Anything
// other than the exact magic, the supported version and a known kind is an
// error.
func readPreamble(r io.Reader) (byte, error) {
	var b [preambleLen]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, fmt.Errorf("uniwire: read preamble: %w", err)
	}
	if string(b[:4]) != preambleMagic {
		return 0, fmt.Errorf("uniwire: bad preamble magic")
	}
	if b[4] != preambleVersion {
		return 0, fmt.Errorf("uniwire: unsupported protocol version %d", b[4])
	}
	switch b[5] {
	case kindRPC, kindStream:
		return b[5], nil
	default:
		return 0, fmt.Errorf("uniwire: unknown connection kind %d", b[5])
	}
}

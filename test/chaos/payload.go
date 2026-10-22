package chaos

import (
	"crypto/sha256"
	"encoding/binary"
)

// Object sizes are drawn uniformly from [minPayloadSize, maxPayloadSize].
const (
	minPayloadSize = 1 << 10
	maxPayloadSize = 64 << 10
)

// Payload is a deterministic object body and its SHA-256 digest.
type Payload struct {
	Body   []byte
	Digest [sha256.Size]byte
}

// NewPayload derives the body for (seed, seq). The same inputs always give the
// same bytes, so a failing run can be reproduced from its seed. The body is a
// SHA-256 counter stream keyed by (seed, seq); the size comes from the same key.
func NewPayload(seed, seq uint64) Payload {
	var in [16]byte
	binary.BigEndian.PutUint64(in[:8], seed)
	binary.BigEndian.PutUint64(in[8:], seq)
	root := sha256.Sum256(in[:])

	span := uint64(maxPayloadSize - minPayloadSize + 1)
	size := minPayloadSize + int(binary.BigEndian.Uint64(root[:8])%span)

	body := make([]byte, size)
	var block [sha256.Size + 8]byte
	copy(block[:sha256.Size], root[:])
	for ctr, off := uint64(0), 0; off < size; ctr++ {
		binary.BigEndian.PutUint64(block[sha256.Size:], ctr)
		sum := sha256.Sum256(block[:])
		off += copy(body[off:], sum[:])
	}
	return Payload{Body: body, Digest: sha256.Sum256(body)}
}

// Package payload provides the object bodies used by the benchmark. Bodies
// are generated once, before any measurement, and reused for every request,
// so random number generation is never part of a measured latency.
package payload

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
)

// Buffer is an immutable random payload. Every request of one size shares the
// same backing array, so callers must treat Bytes as read-only.
type Buffer struct {
	data []byte
}

// NewBuffer generates size random bytes from seed. The same seed and size
// always produce the same bytes. A negative size is an error.
func NewBuffer(size int, seed uint64) (Buffer, error) {
	if size < 0 {
		return Buffer{}, fmt.Errorf("payload size %d is negative", size)
	}
	data := make([]byte, size)
	var key [32]byte
	binary.LittleEndian.PutUint64(key[:8], seed)
	rng := rand.NewChaCha8(key)
	// ChaCha8.Read always fills the whole slice and never fails.
	if _, err := rng.Read(data); err != nil {
		return Buffer{}, fmt.Errorf("fill payload: %w", err)
	}
	return Buffer{data: data}, nil
}

// Len returns the payload size in bytes.
func (b Buffer) Len() int {
	return len(b.data)
}

// Bytes returns the shared payload. Callers must not modify it.
func (b Buffer) Bytes() []byte {
	return b.data
}

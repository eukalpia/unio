package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// DataID identifies the erasure-coded data of one object or one multipart
// part.
type DataID [16]byte

// NewDataID returns a new random DataID.
func NewDataID() DataID {
	var id DataID
	// crypto/rand.Read never returns an error on supported platforms.
	_, _ = rand.Read(id[:])
	return id
}

// IsZero reports whether id is the zero value.
func (id DataID) IsZero() bool { return id == DataID{} }

// String returns the lower-case hex form of id.
func (id DataID) String() string { return hex.EncodeToString(id[:]) }

// ParseDataID parses the hex form produced by DataID.String.
func ParseDataID(s string) (DataID, error) {
	var id DataID
	if err := decodeHex(id[:], s); err != nil {
		return id, fmt.Errorf("ids: data id: %w", err)
	}
	return id, nil
}

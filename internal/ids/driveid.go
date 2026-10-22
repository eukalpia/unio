package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// DriveID identifies a drive for the lifetime of the cluster. It is
// generated when the drive is formatted and stored in its format record.
type DriveID [16]byte

// NewDriveID returns a new random DriveID.
func NewDriveID() DriveID {
	var id DriveID
	_, _ = rand.Read(id[:])
	return id
}

// IsZero reports whether id is the zero value. A zero DriveID in an erasure
// layout marks a shard that was not written.
func (id DriveID) IsZero() bool { return id == DriveID{} }

// String returns the lower-case hex form of id.
func (id DriveID) String() string { return hex.EncodeToString(id[:]) }

// ParseDriveID parses the hex form produced by DriveID.String.
func ParseDriveID(s string) (DriveID, error) {
	var id DriveID
	if err := decodeHex(id[:], s); err != nil {
		return id, fmt.Errorf("ids: drive id: %w", err)
	}
	return id, nil
}

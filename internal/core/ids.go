package core

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// DataID identifies the erasure-coded data of one object or one multipart
// part. It is random, never derived from user input, and safe to use in
// on-disk names in its hex form.
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
	if len(s) != 2*len(id) {
		return id, fmt.Errorf("core: data id %q: want %d hex chars", s, 2*len(id))
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return id, fmt.Errorf("core: data id %q: %w", s, err)
	}
	return id, nil
}

// DriveID identifies a drive for the lifetime of the cluster. It is
// generated when the drive is formatted and stored in its format record.
type DriveID [16]byte

// NewDriveID returns a new random DriveID.
func NewDriveID() DriveID {
	var id DriveID
	_, _ = rand.Read(id[:])
	return id
}

// IsZero reports whether id is the zero value. A zero DriveID in an
// ErasureLayout marks a shard that was not written.
func (id DriveID) IsZero() bool { return id == DriveID{} }

// String returns the lower-case hex form of id.
func (id DriveID) String() string { return hex.EncodeToString(id[:]) }

// ParseDriveID parses the hex form produced by DriveID.String.
func ParseDriveID(s string) (DriveID, error) {
	var id DriveID
	if len(s) != 2*len(id) {
		return id, fmt.Errorf("core: drive id %q: want %d hex chars", s, 2*len(id))
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return id, fmt.Errorf("core: drive id %q: %w", s, err)
	}
	return id, nil
}

// ShardID identifies one erasure shard of one DataID. It is the only key a
// Drive knows; physical placement is private to the drive.
type ShardID struct {
	Data  DataID
	Index uint16
}

// ShardIDSize is the length of the binary encoding of a ShardID.
const ShardIDSize = 18

// String returns "<data-hex>.<index>".
func (s ShardID) String() string { return fmt.Sprintf("%s.%d", s.Data, s.Index) }

// AppendBinary appends the fixed-size, order-preserving binary encoding of s.
func (s ShardID) AppendBinary(b []byte) []byte {
	b = append(b, s.Data[:]...)
	return binary.BigEndian.AppendUint16(b, s.Index)
}

// ParseShardIDBinary decodes the encoding produced by AppendBinary.
func ParseShardIDBinary(b []byte) (ShardID, error) {
	var s ShardID
	if len(b) != ShardIDSize {
		return s, fmt.Errorf("core: shard id: want %d bytes, got %d", ShardIDSize, len(b))
	}
	copy(s.Data[:], b[:16])
	s.Index = binary.BigEndian.Uint16(b[16:])
	return s, nil
}

// NodeID is the stable name of a node, for example "node1". It doubles as
// the Raft server id and address.
type NodeID string

// SetID identifies an erasure set within the cluster.
type SetID uint32

// PartitionID identifies a metadata partition. Partition 0 is the system
// group holding the cluster map, buckets and IAM state.
type PartitionID uint32

// SystemPartition is the id of the system metadata group.
const SystemPartition PartitionID = 0

// BucketID is the internal, never-reused id of a bucket. Object keys are
// stored under the bucket id, so deleting and re-creating a bucket with the
// same name never resurrects old objects.
type BucketID uint64

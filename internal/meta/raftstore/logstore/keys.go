package logstore

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/eukalpia/unio/internal/ids"
)

// Key layout constants. See the package documentation.
const (
	tagLog    byte = 'L'
	tagStable byte = 'S'

	groupLen   = 4
	indexLen   = 8
	logKeyLen  = groupLen + 1 + indexLen
	stableBase = groupLen + 1
)

// groupStart returns the 4-byte big-endian prefix shared by every key of id.
func groupStart(id ids.PartitionID) []byte {
	k := make([]byte, groupLen)
	binary.BigEndian.PutUint32(k, uint32(id))
	return k
}

// tagBounds returns the half-open key range [lower, upper) that holds every
// key of id with the given tag. Tags are single bytes, so the upper bound is
// the group prefix followed by tag+1.
func tagBounds(id ids.PartitionID, tag byte) (lower, upper []byte) {
	lower = append(groupStart(id), tag)
	upper = append(groupStart(id), tag+1)
	return lower, upper
}

// logKey returns the key of the raft log entry at index in group id.
func logKey(id ids.PartitionID, index uint64) []byte {
	k := make([]byte, logKeyLen)
	binary.BigEndian.PutUint32(k, uint32(id))
	k[groupLen] = tagLog
	binary.BigEndian.PutUint64(k[groupLen+1:], index)
	return k
}

// stableKey returns the key of the stable value key in group id.
func stableKey(id ids.PartitionID, key []byte) []byte {
	k := make([]byte, stableBase+len(key))
	binary.BigEndian.PutUint32(k, uint32(id))
	k[groupLen] = tagStable
	copy(k[stableBase:], key)
	return k
}

// parseLogKey returns the group and index of a stored log key. It rejects
// keys of any other shape.
func parseLogKey(key []byte) (ids.PartitionID, uint64, error) {
	if len(key) != logKeyLen || key[groupLen] != tagLog {
		return 0, 0, fmt.Errorf("key of %d bytes with tag %#x is not a log key", len(key), keyTag(key))
	}
	id := ids.PartitionID(binary.BigEndian.Uint32(key))
	return id, binary.BigEndian.Uint64(key[groupLen+1:]), nil
}

// parseKey returns the group of any stored key and checks its shape. It
// rejects keys that no version of this store writes.
func parseKey(key []byte) (ids.PartitionID, error) {
	if len(key) < stableBase {
		return 0, fmt.Errorf("key of %d bytes is shorter than the group prefix", len(key))
	}
	switch key[groupLen] {
	case tagLog:
		if _, _, err := parseLogKey(key); err != nil {
			return 0, err
		}
	case tagStable:
	default:
		return 0, fmt.Errorf("key of %d bytes has unknown tag %#x", len(key), key[groupLen])
	}
	return ids.PartitionID(binary.BigEndian.Uint32(key)), nil
}

// keyTag returns the tag byte of key, or 0 when key is too short to have one.
func keyTag(key []byte) byte {
	if len(key) <= groupLen {
		return 0
	}
	return key[groupLen]
}

// logEndExclusive returns the exclusive end key of a range delete that removes
// the log entries of id with indices up to and including max.
func logEndExclusive(id ids.PartitionID, max uint64) []byte {
	if max == math.MaxUint64 {
		_, upper := tagBounds(id, tagLog)
		return upper
	}
	return logKey(id, max+1)
}

// Package segment implements the append-only segment files that hold packed
// shards (architecture section 8, ADR 0005). A segment is a sequence of
// records. Each record is a fixed-size header followed by the shard bytes.
//
// Record layout, integers big-endian:
//
//	offset  size  field
//	0       4     magic "UNSR"
//	4       1     version (1)
//	5       1     flags (bit 0: tombstone)
//	6       2     reserved, must be zero
//	8       18    ShardID
//	26      8     data length
//	34      4     CRC-32C of the data
//	38      4     CRC-32C of bytes 0..37
//
// Records are self-delimiting and self-verifying, so a segment can be
// scanned without an index. Tombstone data holds the id of the segment that
// contained the deleted record (see TombstoneOrigin).
package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"github.com/eukalpia/unio/internal/ids"
)

const (
	// HeaderSize is the encoded size of a record header.
	HeaderSize = 42
	// Version is the record format version written by this package.
	Version = 1
	// MaxDataLength bounds the data length accepted from disk. It guards
	// allocations made from untrusted lengths.
	MaxDataLength = 1 << 30
)

var (
	magic    = [4]byte{'U', 'N', 'S', 'R'}
	crcTable = crc32.MakeTable(crc32.Castagnoli)
)

// Flags are record flags.
type Flags uint8

// Record flags.
const (
	// FlagTombstone marks a record that deletes its ShardID.
	FlagTombstone Flags = 1 << 0

	knownFlags = FlagTombstone
)

// Errors returned by this package.
var (
	// ErrInvalid reports a record that fails structural or checksum
	// verification. It also covers a torn record at the end of a segment.
	ErrInvalid = errors.New("segment: invalid record")
	// ErrFull reports an append that would exceed the segment capacity.
	ErrFull = errors.New("segment: full")
)

// Header describes one record.
type Header struct {
	Flags   Flags
	ShardID ids.ShardID
	Length  uint64
	DataCRC uint32
}

// RecordSize returns the on-disk size of a record whose data is n bytes.
func RecordSize(n int64) int64 { return HeaderSize + n }

// Checksum returns the CRC-32C used for record data and headers.
func Checksum(data []byte) uint32 { return crc32.Checksum(data, crcTable) }

// AppendHeader appends the encoded header, including its own checksum, to dst.
func (h Header) AppendHeader(dst []byte) []byte {
	start := len(dst)
	dst = append(dst, magic[:]...)
	dst = append(dst, Version, byte(h.Flags), 0, 0)
	dst = h.ShardID.AppendBinary(dst)
	dst = binary.BigEndian.AppendUint64(dst, h.Length)
	dst = binary.BigEndian.AppendUint32(dst, h.DataCRC)
	return binary.BigEndian.AppendUint32(dst, crc32.Checksum(dst[start:], crcTable))
}

// DecodeHeader parses and verifies the first HeaderSize bytes of b. It
// returns an error wrapping ErrInvalid for any structural or checksum failure.
func DecodeHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, fmt.Errorf("%w: short header (%d bytes)", ErrInvalid, len(b))
	}
	b = b[:HeaderSize]
	if [4]byte(b[:4]) != magic {
		return Header{}, fmt.Errorf("%w: bad magic", ErrInvalid)
	}
	if b[4] != Version {
		return Header{}, fmt.Errorf("%w: unsupported version %d", ErrInvalid, b[4])
	}
	flags := Flags(b[5])
	if flags&^knownFlags != 0 {
		return Header{}, fmt.Errorf("%w: unknown flags %#x", ErrInvalid, b[5])
	}
	if b[6] != 0 || b[7] != 0 {
		return Header{}, fmt.Errorf("%w: reserved bytes set", ErrInvalid)
	}
	if crc32.Checksum(b[:38], crcTable) != binary.BigEndian.Uint32(b[38:]) {
		return Header{}, fmt.Errorf("%w: header checksum mismatch", ErrInvalid)
	}
	length := binary.BigEndian.Uint64(b[26:34])
	if length > MaxDataLength {
		return Header{}, fmt.Errorf("%w: length %d exceeds limit", ErrInvalid, length)
	}
	var h Header
	h.Flags = flags
	copy(h.ShardID.Data[:], b[8:24])
	h.ShardID.Index = binary.BigEndian.Uint16(b[24:26])
	h.Length = length
	h.DataCRC = binary.BigEndian.Uint32(b[34:38])
	return h, nil
}

// AppendRecord appends a complete record (header and data) to dst.
func AppendRecord(dst []byte, flags Flags, id ids.ShardID, data []byte) []byte {
	h := Header{Flags: flags, ShardID: id, Length: uint64(len(data)), DataCRC: Checksum(data)}
	dst = h.AppendHeader(dst)
	return append(dst, data...)
}

// TombstoneOrigin returns the data of a tombstone that records the segment
// which held the deleted record.
func TombstoneOrigin(segmentID uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, segmentID)
}

// ParseTombstoneOrigin decodes the data written by TombstoneOrigin.
func ParseTombstoneOrigin(data []byte) (uint64, error) {
	if len(data) != 8 {
		return 0, fmt.Errorf("%w: tombstone payload is %d bytes", ErrInvalid, len(data))
	}
	return binary.BigEndian.Uint64(data), nil
}

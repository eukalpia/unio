package ids

import (
	"encoding/binary"
	"fmt"
	"strconv"
)

// ShardID identifies one erasure shard of one DataID. It is the only key a
// drive knows; physical placement is private to the drive.
type ShardID struct {
	Data  DataID
	Index uint16
}

// ShardIDSize is the length of the binary encoding of a ShardID.
const ShardIDSize = 18

// String returns "<data-hex>.<index>".
func (s ShardID) String() string {
	return s.Data.String() + "." + strconv.Itoa(int(s.Index))
}

// AppendBinary appends the fixed-size, order-preserving binary encoding of s.
func (s ShardID) AppendBinary(b []byte) []byte {
	b = append(b, s.Data[:]...)
	return binary.BigEndian.AppendUint16(b, s.Index)
}

// ParseShardIDBinary decodes the encoding produced by AppendBinary.
func ParseShardIDBinary(b []byte) (ShardID, error) {
	var s ShardID
	if len(b) != ShardIDSize {
		return s, fmt.Errorf("ids: shard id: want %d bytes, got %d", ShardIDSize, len(b))
	}
	copy(s.Data[:], b[:16])
	s.Index = binary.BigEndian.Uint16(b[16:])
	return s, nil
}

package bitrot

import (
	"encoding/binary"

	"github.com/zeebo/xxh3"

	"github.com/eukalpia/unio/internal/ids"
)

// Position is the place a shard block belongs to. Every block digest is bound
// to its position, so a block read from any other place fails verification
// even when its own bytes are intact: a block from another offset, another
// shard index, another object, or a stale copy of an older write.
type Position struct {
	// Data identifies the object (its DataID).
	Data ids.DataID
	// Shard is the index of the shard within the object.
	Shard uint16
	// FirstBlock is the block index of the first block a Writer or Reader
	// handles. The block index of a block is its stripe index, and it grows
	// by one for each block.
	FirstBlock uint64
}

// positionKeyLen is the length of the key that seeds a block digest:
// DataID (16) || BE16(shard) || BE64(block).
const positionKeyLen = 16 + 2 + 8

// blockSeed returns the 64-bit xxh3 seed of the block at the given position:
// xxh3-64 of DataID || BE16(shard) || BE64(block). The key is a fixed-size
// array, so no allocation is needed and the data is never copied.
func blockSeed(id ids.DataID, shard uint16, block uint64) uint64 {
	var key [positionKeyLen]byte
	copy(key[:16], id[:])
	binary.BigEndian.PutUint16(key[16:18], shard)
	binary.BigEndian.PutUint64(key[18:26], block)
	return xxh3.Hash(key[:])
}

// digest returns the digest of payload as the block with index block at
// position p: xxh3-128 of the payload, seeded by the position.
func digest(p Position, block uint64, payload []byte) [HashSize]byte {
	return xxh3.Hash128Seed(payload, blockSeed(p.Data, p.Shard, block)).Bytes()
}

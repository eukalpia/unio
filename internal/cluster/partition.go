package cluster

import (
	"encoding/binary"

	"github.com/zeebo/xxh3"

	"github.com/eukalpia/unio/internal/ids"
)

// PartitionOf maps an object key to its data partition in 1..partitions.
// Every node must use this function so that routing agrees cluster-wide.
func PartitionOf(bucket ids.BucketID, key string, partitions int) ids.PartitionID {
	if partitions <= 0 {
		return ids.SystemPartition
	}
	var seed [8]byte
	binary.LittleEndian.PutUint64(seed[:], uint64(bucket))
	h := xxh3.HashSeed([]byte(key), xxh3.Hash(seed[:]))
	return ids.PartitionID(h%uint64(partitions)) + 1
}

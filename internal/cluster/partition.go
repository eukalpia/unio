package cluster

import (
	"encoding/binary"

	"github.com/zeebo/xxh3"

	"github.com/eukalpia/unio/internal/ids"
)

// MaxPartitions bounds the number of data partitions in a cluster.
const MaxPartitions = 4096

// PartitionOf maps an object key to its data partition in 1..partitions.
// Every node must use this function so that routing agrees cluster-wide.
// partitions must be in 1..MaxPartitions, which cluster map validation
// guarantees; any other value maps to the system partition.
func PartitionOf(bucket ids.BucketID, key string, partitions int) ids.PartitionID {
	if partitions <= 0 || partitions > MaxPartitions {
		return ids.SystemPartition
	}
	var seed [8]byte
	binary.LittleEndian.PutUint64(seed[:], uint64(bucket))
	h := xxh3.HashSeed([]byte(key), xxh3.Hash(seed[:]))
	// The remainder is below MaxPartitions, so the conversion cannot overflow.
	return ids.PartitionID(uint32(h%uint64(partitions))) + 1 // #nosec G115 -- bounded by MaxPartitions
}

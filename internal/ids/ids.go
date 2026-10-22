package ids

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

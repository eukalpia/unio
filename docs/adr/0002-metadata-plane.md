# 0002. Separate metadata plane on Multi-Raft and Pebble

- Status: Accepted
- Date: 2026-10-22

## Context

Storing metadata next to object data on every drive (MinIO's `xl.meta`
approach) has two consequences:

- Listing requires walking directories on many drives and merging the
  results under quorum, which degrades badly with millions of keys.
- Small objects cost a directory and a file per drive.

## Decision

- Object metadata lives in a dedicated plane: P hash partitions (default 16,
  fixed at cluster creation).
- Each partition is a hashicorp/raft group of min(3, nodes) replicas backed
  by a Pebble database.
- System state (cluster map, buckets, IAM) lives in group 0, which is
  replicated to every node, with non-voters where needed.
- Keys are order-preserving: `O | bucketID | escaped(key) | ^versionTimestamp`.
- LIST is a parallel range scan with a k-way merge and delimiter skip-seek.
- Linearizable reads use coalesced `VerifyLeader` rounds.

## Consequences

- LIST and HEAD are KV operations, giving orders-of-magnitude faster
  listings, and conditional writes become atomic.
- An additional replicated subsystem to operate and test. Linearizability is
  verified with porcupine.
- Hash partitioning makes LIST fan out to every partition. Range
  partitioning with splits is planned behind the same `MetaStore` interface.

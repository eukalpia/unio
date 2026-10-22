# 0005. Size-tiered data layout with logical shard ids

- Status: Accepted
- Date: 2026-10-22

## Context

One file per shard wastes inodes and IOPS on small objects. Packing shards
into segments requires compaction. If metadata stored physical offsets,
every compaction would have to write to Raft.

## Decision

Data is placed in one of three tiers by object size:

| Tier | Object size | Storage |
|---|---|---|
| Inline | ≤ 4 KiB by default, configurable up to 64 KiB | In the metadata record |
| Packed | ≤ 1 MiB | Shards appended to 256 MiB segments with group fsync |
| Blob | > 1 MiB, and all multipart parts | One file per shard |

- Metadata references a logical `ShardID` (DataID, part number, shard index).
- Each drive keeps a local Pebble index from `ShardID` to its physical
  location.
- On-disk names are hex ids only, never user input.

## Consequences

- Small-object writes are amortised, and compaction is local to a drive.
- Reads pay one local index lookup per shard.
- The index can be rebuilt from segment record headers.

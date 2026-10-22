# UniIO Architecture

- Status: accepted (v0.1 baseline)
- Last updated: 2026-10-22
- Decision records: [docs/adr](../adr/README.md)

This document describes the target architecture of UniIO for the v0.1
milestone and the constraints every contribution must respect. Changes to
anything described here go through an [ADR](../adr/README.md).

## 1. Goals

UniIO is a distributed, S3-compatible object store. It aims to be a universal
system: at least as fast as MinIO on large objects, clearly faster on small
objects and listing, and safe and stable from the first release.

Priorities. All three are mandatory; when they conflict, this order wins:

1. **Security and data integrity.** An acknowledged write is never lost,
   corrupted data is never served, and all internode traffic is encrypted and
   authenticated.
2. **Stability.** Bounded memory, fault tolerance, correct behaviour when
   nodes and drives fail, no panics escaping to clients.
3. **Performance.** Measured with benchmarks and guarded against regressions.

### 1.1 Targets

These are goals to be validated in the v0.2 performance milestone, not claims.

| Metric | Target | Measured with |
|---|---|---|
| S3 compatibility | ≥ 90% of the v0.1 subset of `ceph/s3-tests` | CI |
| PUT/GET 64 MiB throughput | ≥ MinIO (goal +10%) | `warp`, same hardware, 4 nodes × 4 drives |
| PUT 4 KiB ops/s | ≥ 2× MinIO | `warp` |
| GET 4 KiB ops/s | ≥ 1.5× MinIO | `warp` |
| LIST p99 (1000-key page, 10M-object bucket, prefix + delimiter) | ≥ 10× faster than MinIO | project benchmark |
| Memory | bounded by configuration; no OOM at 10k connections | load test |

### 1.2 Correctness invariants

Every invariant is covered by automated tests.

- **I1 Durability.** An object whose PUT returned 200 survives a simultaneous
  `kill -9` of every node (data is fsynced before the response). It stays
  readable while at least K shards and a metadata quorum of its partition are
  available.
- **I2 Integrity.** A client never receives bytes that differ from what was
  written. Every block is verified against its bitrot hash on read. On a
  mismatch UniIO reconstructs from parity or returns an error, never garbage.
- **I3 Linearizable metadata.** After an acknowledged PUT or DELETE, any
  GET, HEAD or LIST on any node observes that result or a newer one.
- **I4 Atomicity.** A failed or interrupted PUT never becomes visible. Its
  shards are removed by background garbage collection.
- **I5 Safe overwrite.** Data of a previous version is deleted only after
  the metadata of the new version is committed.

## 2. Overview

UniIO ships as one binary, `uniio`. All nodes are symmetric and run the same
roles, so operations stay simple. Internally, data and metadata are separate
planes.

```
                S3 clients (AWS SDKs, rclone, Spark, Trino, ...)
                                 │ HTTP(S) / S3, port 9000
┌──────────────────────── UniIO node (single binary) ──────────────────────┐
│  s3api (Hertz, standard transport)  ──  auth (SigV4, IAM, policies)      │
│          │                                                               │
│  object: inline / packed / blob placement, multipart, copy, delete       │
│      │                                     │                             │
│  meta: Multi-Raft partitions on Pebble   erasure: Reed-Solomon + bitrot  │
│      │                                     │                             │
│  cluster: UniWire (mTLS, port 9100) — RPC, shard streams, Raft           │
│      │                                     │                             │
│  cluster map, membership, PKI            storage: drive engine           │
│                                          (packed segments + blob files)  │
└──────────────────────────────────────────────────────────────────────────┘
```

### 2.1 Technology choices

| Layer | Choice | Rationale |
|---|---|---|
| S3 API | CloudWeGo **Hertz**, standard transport | Hertz supports TLS only on the standard transport. On netpoll, request-body streaming buffers the whole body in memory. Hertz is benchmarked against `net/http` in v0.2 ([ADR-0004](../adr/0004-hertz-standard-transport.md)) |
| JSON | **bytedance/sonic** with automatic fallback | IAM policies, admin API |
| XML | Hand-written encoder for hot responses; size-limited `encoding/xml` for small request bodies | `encoding/xml` is slow for `ListObjects` |
| Metadata | **hashicorp/raft** (one group per partition) + **cockroachdb/pebble** | Mature, pure Go. LIST becomes an ordered range scan ([ADR-0002](../adr/0002-metadata-plane.md)) |
| Erasure coding | **klauspost/reedsolomon** | SIMD (AVX2, AVX-512, GFNI), production-proven |
| Bitrot hash | **xxh3-128** | Tens of GB/s per core |
| S3 checksums | MD5 (ETag), SHA-256, CRC32, CRC32C, CRC64NVME, SHA-1 | Current AWS SDKs send CRC trailers by default |
| Serialization | **protobuf** + **vtprotobuf** | Schema evolution and reflection-free (un)marshalling ([ADR-0006](../adr/0006-protobuf-encoding.md)) |
| Internode transport | **UniWire**, our own protocol over `crypto/tls` with mTLS | Kitex/netpoll has no native TLS. A dedicated protocol also cuts copies on shard streams ([ADR-0003](../adr/0003-uniwire-internode-transport.md)) |
| Metrics | prometheus/client_golang | De facto standard |
| Logging | `log/slog`, JSON | Standard library, structured |

Release builds are `CGO_ENABLED=0` and built with `-trimpath`.

## 3. Topology and placement

- **Erasure sets.** At cluster initialisation, all drives are partitioned
  into erasure sets of 4–16 drives, spread as evenly as possible across
  nodes: no more than ⌈(K+M)/N⌉ drives of a set on any node.
- **Default parity.** M = 2 for sets of up to 8 drives, M = 4 for 9–16
  drives. Parity is configurable.
- **Deployment modes:**
  - 1 node × 1 drive: no erasure coding (K=1, M=0); for development only.
  - 1 node × N drives: erasure coding across drives.
  - 3 or more nodes: full high availability.
  - 2 nodes: allowed, but metadata is not fault tolerant. UniIO logs a
    warning.
- **Recorded placement.** Placement is recorded in the object's metadata
  (erasure set id and drive ids) instead of being recomputed from a hash on
  every read.
  - New objects pick a set by key hash, skipping sets that are more than
    90% full.
  - This lets the cluster expand and rebalance without MinIO-style server
    pools or decommissioning.

## 4. Size-tiered data layout

| Object size | Where the data lives | Why |
|---|---|---|
| ≤ 4 KiB (configurable up to 64 KiB) | Inline in the metadata record, replicated by Raft | One write instead of K+M drive operations |
| ≤ 1 MiB | Shards packed into append-only **segments** (256 MiB) | No inode per object; group fsync |
| > 1 MiB, and every multipart part | One **blob file** per shard | Deletion is an unlink; no compaction |

Metadata stores a **logical `ShardID`** (DataID + part number + shard index),
never a physical offset. Every drive maintains its own local index (Pebble)
mapping `ShardID → (segment, offset, length)` or `ShardID → blob`. Segment
compaction updates only the local index and never writes to Raft. See
[ADR-0005](../adr/0005-size-tiered-layout.md).

## 5. Data paths

### 5.1 PUT

1. **Parse and authenticate.**
   - `s3api` parses the request.
   - `auth` verifies the signature and returns the body as a verifying
     reader. It handles aws-chunked encoding, chunk signatures, trailer
     checksums, the SHA-256 of signed payloads, and Content-MD5.
   - The reader returns an error **before** EOF if any check fails.
2. **Write the data.** `object` chooses the storage class by size.
   - **Inline:** read the whole body (bounded by the limit) and commit the
     metadata.
   - **Otherwise:**
     - Generate a random 128-bit `DataID` and choose an erasure set.
     - `erasure` cuts the stream into 1 MiB stripes and encodes K+M shards.
     - Each shard block is framed with its bitrot hash.
     - Shards are written to all drives of the set in parallel: local drives
       directly, remote drives over UniWire.
3. **Reach the write quorum.**
   - Write quorum is K, or K+1 when K = M.
   - A shard counts as written only after fsync; segments use group fsync.
   - If the quorum is not reached, the client gets an error and any written
     shards are queued for deletion.
4. **Commit metadata.**
   - `object` commits only if the body was read to a clean EOF (every
     integrity check passed) and a write quorum of shards was persisted.
   - The commit goes through the Raft partition owning the key.
   - Conditional writes (`If-Match`, `If-None-Match`) are evaluated
     atomically inside the state machine.
5. **Clean up the old version.** If the commit replaced an older version,
   that version's shards are queued for deletion (I5). If the node crashes
   before deleting them, garbage collection finds them.

### 5.2 GET

1. Perform a linearizable metadata read on the partition leader.
2. For inline objects, serve the data from the record.
3. Otherwise:
   - Read only the **K data shards** in parallel, preferring local drives.
     The code is systematic, so no decoding is needed.
   - Verify every block as it is read.
4. On a read error or hash mismatch:
   - read parity shards and reconstruct;
   - serve the correct bytes;
   - queue the damaged shard for repair (heal-on-read).
5. A Range GET reads only the blocks it touches.

## 6. Metadata plane

- **Partitions.** Objects are distributed over P partitions by
  `hash(bucketID, key)`. P is fixed at cluster creation and defaults to 16.
  - Each partition is a Raft group of min(3, nodes) replicas with its own
    Pebble database. A node shares one block cache across its databases.
  - All Raft logs on a node live in one shared Pebble database.
- **System group 0** holds the cluster map, buckets and IAM.
  - It is replicated to every node; nodes outside the voting quorum hold
    non-voter replicas.
  - Bucket and access-key checks are therefore local reads. On a miss, UniIO
    re-checks linearizably against the leader.
- **Key schema** inside a partition:
  `O | bucketID | escaped(key) | ^versionTimestamp`.
  - The encoding preserves order, and versions of a key sort newest first.
  - Versioning is part of the schema from day one; the versioning API ships
    in v0.3.
- **LIST** runs a parallel range scan over all partitions with a lazy,
  batched k-way merge.
  - Delimiter queries use skip-seek: after seeing the common prefix `a/b/`,
    the iterator jumps straight past it.
- **Linearizable reads** use `VerifyLeader`. Concurrent callers are coalesced
  into a single heartbeat round.
- **Future.** Range partitioning with automatic splits for very large
  buckets. The `MetaStore` interface hides the partitioning scheme.

## 7. UniWire internode protocol

- A single TCP port (9100), **always mTLS** with TLS 1.3.
  - The only way to disable it is the explicit `--insecure-internode` flag,
    meant for benchmarks; using it prints a loud warning.
- **Trust bootstrap from a shared secret.**
  - An Ed25519 cluster CA key is derived deterministically via HKDF from
    `UNIIO_CLUSTER_SECRET` (at least 32 characters).
  - Each node issues its own certificate from that CA and accepts only peers
    with certificates from the same CA.
  - Operators may supply their own certificates instead.
- A connection begins with a preamble that selects its kind:
  - **RPC:** multiplexed requests with ids, protobuf payloads, deadlines and
    cancellation.
  - **Stream:** one shard stream per pooled connection; a protobuf header
    followed by raw bytes of known length.
  - **Raft:** an implementation of `raft.Transport`. AppendEntries goes over
    RPC; InstallSnapshot goes over Stream.
- Limits: maximum frame size, connections per peer, idle timeouts and
  handshake timeouts.

## 8. Drive engine

- **Layout:**
  - `<drive>/.uniio/format.pb`: drive id, cluster id, format version;
  - `segments/`: packed segments;
  - `blobs/<xx>/<id>`: blob files;
  - `index/`: the local Pebble index;
  - `tmp/`.
- **On-disk paths are never derived from user keys.** Only hex ids are used,
  so path traversal is impossible by construction.
- **Segment record:** `[header: magic, version, ShardID, length, crc32c] [data]`.
  The index can be rebuilt by scanning segments.
- **Group commit:** concurrent appends to a segment share one `fdatasync`.
  This is the main accelerator for small objects.
- **Compaction:** segments with more than 50% garbage are rewritten in the
  background under an I/O budget.
- **Linux optimisations** (build tags):
  - `O_DIRECT` with aligned buffers for blobs;
  - `fallocate` for segments.
  - macOS and Windows use buffered fallbacks.
- **Sync policy:** `always` (default) or `batch`.

## 9. S3 API scope for v0.1

- **Buckets:** Create, Delete, Head, List, GetBucketLocation.
- **Objects:**
  - PutObject, GetObject (Range and conditional headers), HeadObject;
  - DeleteObject, DeleteObjects (up to 1000 keys);
  - CopyObject;
  - ListObjectsV2 and ListObjects (V1).
- **Multipart:** Create, UploadPart, UploadPartCopy, Complete, Abort,
  ListParts, ListMultipartUploads.
- **Authentication:**
  - SigV4 in the header and presigned URLs;
  - `STREAMING-AWS4-HMAC-SHA256-PAYLOAD`, with and without a trailer;
  - `STREAMING-UNSIGNED-PAYLOAD-TRAILER` and `UNSIGNED-PAYLOAD`.
- **Checksums:** `x-amz-checksum-*` with CRC32, CRC32C, CRC64NVME, SHA1 and
  SHA256.
- **Object attributes:** user metadata, object tags, `Content-*` headers.
- **Admin API** (`/uniio/admin/v1/...`, SigV4-signed with an admin key):
  - users and access keys;
  - cluster information;
  - triggering repair.
- **Service endpoints:** `/uniio/health/live`, `/uniio/health/ready`, and
  `/uniio/metrics` (token-protected).

Planned for later milestones (see [ROADMAP](../../ROADMAP.md)):

- versioning API, lifecycle, full bucket policies;
- replication, SSE encryption, object lock, event notifications;
- web console;
- STS, OIDC and LDAP;
- metadata range splits, rebalancing and online expansion;
- kTLS, io_uring and S3 Select.

## 10. Security baseline

- **No default credentials.**
  - The root key must be provided, or `uniio init` generates a random one
    and writes it to a file with mode 0600.
  - Weak keys are rejected: access keys shorter than 12 characters and
    secret keys shorter than 32.
- **Secrets at rest.** User secret keys are stored in the system group
  encrypted with AES-256-GCM. The key-encryption key is derived from the
  cluster secret via HKDF.
- **Signatures.**
  - Signatures are compared only in constant time (`hmac.Equal`).
  - SigV4 allows up to 15 minutes of clock skew.
- **Encryption in transit.**
  - All internode traffic uses mTLS (section 7).
  - The S3 endpoint serves HTTPS when certificates are present. Plain HTTP
    logs a warning, and `--require-tls` forbids it.
- **Input validation:**
  - keys ≤ 1024 bytes of valid UTF-8; bucket names follow S3 rules;
  - user metadata ≤ 2 KiB; request headers ≤ 64 KiB;
  - XML bodies ≤ 2 MiB;
  - objects ≤ 5 TiB; parts ≤ 5 GiB; at most 10,000 parts.
- **DoS protection:**
  - header, body and idle timeouts (slowloris);
  - a concurrency limit with a bounded queue, returning `503 SlowDown` when
    it is exceeded;
  - a global memory budget for buffers.
- **Failure isolation.**
  - A panic inside a handler is recovered: the client gets
    `500 InternalError` and the event is logged with its stack.
  - Internal error details are never returned to clients.
- **Fuzzing** (Go native) for every parser:
  - SigV4 and the Authorization header;
  - the aws-chunked decoder;
  - XML bodies and key validation;
  - UniWire frames and segment records.
- **Supply chain.**
  - `govulncheck`, `gosec` and `staticcheck` run in CI.
  - Dependency versions are pinned.
  - Builds use `-trimpath`, with no CGO.
- An access log with a request id for every request.

## 11. Stability baseline

- **Bounded memory.**
  - A global buffer pool (`bufpool`) has a hard limit; requests wait for
    buffers, subject to their deadline.
  - There are no unbounded queues or goroutine fan-outs.
  - Every background job (GC, compaction, repair) has a concurrency and I/O
    budget.
- `context.Context` is propagated everywhere. Client cancellation stops all
  work done on that request's behalf.
- **Graceful shutdown** drains requests, transfers Raft leadership and closes
  drives.
- **Resilience tests:**
  - `goleak` in every package that starts goroutines;
  - crash tests: PUT loops interrupted by `kill -9`, then I1 and I4 are
    checked after restart;
  - metadata linearizability checking with **porcupine** on histories that
    include leader failures;
  - drive fault injection (errors, flipped bytes, slow drives) to verify I2
    and heal-on-read.

## 12. Performance baseline

- **Allocation-free hot paths:**
  - pooled buffers;
  - reflection-free vtprotobuf;
  - a hand-written XML encoder;
  - SIMD xxh3 and Reed-Solomon.
- **Parallelism:**
  - shard reads and writes run in parallel;
  - reading and encoding are pipelined;
  - GET of a healthy object needs no decoding.
- **Small objects:** inline metadata, segments with group fsync, batched
  Raft applies.
- **LIST:** range scans with skip-seek instead of filesystem walks.
- **Benchmarks:**
  - `go test -bench` covers every hot path and is compared with `benchstat`
    in CI;
  - cluster-level comparisons against MinIO use `warp`.
- `pprof` is served only behind authentication.

## 13. Module layout

```
cmd/uniio/                   CLI: server, init, admin
api/proto/uniio/v1/          Protobuf: metadata records, Raft commands, RPC
internal/pb/                 Generated protobuf / vtprotobuf code
internal/core/               Shared types, errors and interfaces (contracts)
internal/core/coretest/      In-memory fakes and conformance suites
internal/bufpool/            Bounded buffer pool
internal/hashreader/         Verifying reader: MD5 / SHA-256 / CRC*
internal/s3api/              Hertz server, routing, XML, S3 errors
internal/auth/               SigV4 (all variants), IAM, policies
internal/erasure/            EC pipeline, bitrot, quorums, heal-on-read
internal/storage/            Drive engine
internal/meta/               Multi-Raft, state machines, Pebble schema, LIST
internal/cluster/            UniWire, PKI, remote drives, Raft transport,
                             cluster map, health
internal/object/             Object layer orchestration
internal/config/             Configuration
internal/obs/                Logging and metrics
test/integration/            End-to-end tests (AWS SDK for Go v2, s3-tests)
test/chaos/                  Crash, partition and fault-injection tests
bench/                       Benchmark harness
deploy/                      Docker and Compose files
```

### 13.1 Contracts

`internal/core` defines the interfaces between modules:

| Interface | Purpose | Implemented by |
|---|---|---|
| `ObjectLayer` | Buckets, objects, multipart and listing, as called by `s3api` | `object` |
| `Drive` | Write, read and delete a shard by `ShardID`; stats; shard enumeration for GC and repair | `storage` (local), `cluster` (remote over UniWire) |
| `ErasureSet` | Streaming write and read of objects and parts across a set of `Drive`s with quorums | `erasure` |
| `MetaStore` | Buckets, object versions, multipart state, listing, conditional commits | `meta` |
| `Authenticator` / `IAM` | Verify a request and hand out the body reader; users and keys | `auth` |
| `Topology` | Cluster map, erasure sets, partition routing | `cluster` |

Each interface ships with an in-memory fake and a conformance suite in
`internal/core/coretest`, for example `coretest.RunDriveSuite(t, factory)`.
Every real implementation must pass the same suite as its fake.

Changing a contract requires an ADR and maintainer review, because it affects
every module that depends on it.

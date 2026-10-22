<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/unio-mark-dark.svg">
    <img src="assets/logo/unio-mark.svg" alt="Unio logo" width="112">
  </picture>
</p>

<h1 align="center">Unio</h1>

<p align="center"><strong>A distributed, S3-compatible object store built for speed, integrity and simple operations.</strong></p>

[![CI](https://github.com/eukalpia/unio/actions/workflows/ci.yml/badge.svg)](https://github.com/eukalpia/unio/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

> **Status: pre-alpha.** The architecture is fixed and the v0.1 foundation is
> under active development. Do not store production data yet.

Unio is a single Go binary that turns a set of servers and drives into a
fault-tolerant object store with an S3 API. It aims to be universal: competitive
on large objects, and much faster on small objects and listing than file-walking
designs.

## Why Unio

| | Unio |
|---|---|
| **Fast listing** | A dedicated metadata plane: Raft-replicated partitions on Pebble. LIST is an ordered range scan, not a filesystem walk |
| **Small objects** | Tiny objects are stored inline in metadata. Small objects are packed into append-only segments with group fsync, with no inode per object |
| **Large objects** | Reed-Solomon erasure coding with SIMD and parallel, pipelined shard I/O. Healthy reads skip decoding |
| **Integrity** | Every block carries an xxh3 bitrot hash, verified on every read. Damaged shards are repaired on read |
| **Consistency** | Linearizable metadata and atomic conditional writes (`If-Match`, `If-None-Match`) |
| **Security by default** | mTLS on every internode connection, no default credentials, secrets encrypted at rest, fuzzed parsers |
| **Simple operations** | One binary, symmetric nodes, placement recorded in metadata for future rebalancing |
| **Permissive license** | Apache License 2.0 |

## Architecture at a glance

```
S3 clients ──► s3api (Hertz) ──► auth (SigV4/IAM)
                    │
                 object layer ──► meta (Multi-Raft + Pebble)
                    │
                 erasure (Reed-Solomon + bitrot) ──► storage (segments + blobs)
                    │
                 cluster (UniWire: mTLS RPC, shard streams, Raft)
```

Read the full design in [docs/design/architecture.md](docs/design/architecture.md).
Key decisions are recorded in [docs/adr](docs/adr/README.md).

## Technology

- Go 1.25, pure Go, no CGO
- [CloudWeGo Hertz](https://github.com/cloudwego/hertz) for the S3 API
- [bytedance/sonic](https://github.com/bytedance/sonic) for JSON
- [hashicorp/raft](https://github.com/hashicorp/raft) and
  [cockroachdb/pebble](https://github.com/cockroachdb/pebble) for metadata
- [klauspost/reedsolomon](https://github.com/klauspost/reedsolomon) for
  erasure coding
- [xxh3](https://github.com/zeebo/xxh3) for bitrot protection
- Protobuf with [vtprotobuf](https://github.com/planetscale/vtprotobuf)
- UniWire, an mTLS-only internode protocol

## Roadmap

| Milestone | Scope |
|---|---|
| **v0.1 Foundation** | Core S3 API, erasure coding, drive engine, Raft metadata, mTLS cluster, CLI, integration tests |
| **v0.2 Performance** | Benchmarks against MinIO, profiling, tuning to the published targets |
| **v0.3 Data management** | Versioning, lifecycle, bucket policies, web console |
| **v0.4 Enterprise** | Replication, SSE, object lock, notifications, STS/OIDC/LDAP, online expansion |

Details: [ROADMAP.md](ROADMAP.md).

## Getting started

Runnable builds arrive with v0.1. To build from source:

```sh
git clone https://github.com/eukalpia/unio.git
cd unio
go build ./cmd/unio
```

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) for the
development setup, branch and commit conventions, and quality gates. Please
follow our [Code of Conduct](CODE_OF_CONDUCT.md).

Report security issues privately as described in [SECURITY.md](SECURITY.md).

## License

Unio is licensed under the [Apache License, Version 2.0](LICENSE).

Unio is an independent project and is not affiliated with or endorsed by
MinIO, Inc. or Amazon Web Services. "S3" and "Amazon S3" are trademarks of
Amazon.com, Inc. or its affiliates.

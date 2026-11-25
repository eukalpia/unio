<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/unio-mark-dark.svg">
    <img src="assets/logo/unio-mark.svg" alt="Unio logo" width="112">
  </picture>
</p>

<h1 align="center">Unio</h1>

<p align="center"><strong>An S3-compatible object store in one Go binary. Fast on small objects and listings, careful with your data.</strong></p>

[![CI](https://github.com/eukalpia/unio/actions/workflows/ci.yml/badge.svg)](https://github.com/eukalpia/unio/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/eukalpia/unio?include_prereleases)](https://github.com/eukalpia/unio/releases)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

Unio turns a set of drives, on one server or many, into an object store with
an S3 API. It is licensed under Apache 2.0.

> **Status: 0.1, early.** Since 25 November 2026 Unio has been the only object
> store of a university learning platform: about 580,000 objects (videos,
> documents, photos), moved from MinIO with `unio-mirror`. The S3 surface is
> the core one (see [What works](#what-works)), and the on-disk format may
> still change before 1.0. Keep backups, as with any young storage system.

## Compared with MinIO

Same machine, same drives, same client, one run after the other
(4 vCPU, one SSD, 1 node x 4 drives, default settings for both):

| Workload | MinIO | Unio | |
|---|---:|---:|---:|
| PUT 4 KiB, 16 clients | 801 ops/s | 8,512 ops/s | **10.6x** |
| GET 4 KiB, 16 clients | 6,511 ops/s | 15,962 ops/s | **2.5x** |
| Mixed GET and PUT, 4 KiB | 1,711 ops/s | 11,493 ops/s | **6.7x** |
| LIST p99, 20,000 keys | 859 ms | 145 ms | **5.9x** |
| PUT 1 MiB | 226 MiB/s | 371 MiB/s | **1.6x** |
| GET 1 MiB | 2,403 MiB/s | 3,678 MiB/s | **1.5x** |
| GET 64 MiB | 5,466 MiB/s | 6,609 MiB/s | **1.2x** |
| PUT 64 MiB | 644 MiB/s | 649 MiB/s | equal (disk-bound) |

The environment, the script, the raw output and what the run does not show
are in [bench/results/2026-11-26](bench/results/2026-11-26/README.md). Run it
on your own hardware before you believe it.

In production, on a VM with one vCPU, the same LMS traffic after the switch
from MinIO: video downloads 882 MB/s instead of 595 MB/s, a seek into a video
2.7 ms instead of 4.5 ms (median), and a 64 KiB upload 3.0 to 3.6 ms instead
of 7.6 ms (median).

## Why it is fast

- **Listing is a range scan.** Object metadata lives in Raft-replicated
  partitions on [Pebble](https://github.com/cockroachdb/pebble), not in files
  next to the data. LIST reads an ordered key range instead of walking
  directories.
- **Small objects have no file of their own.** Objects up to 4 KiB (64 KiB if
  you set `UNIO_INLINE_THRESHOLD`) are stored in the metadata record. Larger
  small objects are packed into append-only segments with group fsync. No
  inode per object.
- **Large objects stream.** Reed-Solomon erasure coding with SIMD, parallel
  shard I/O, and a healthy read skips decoding.

## Why it is careful

- **Every block is checked on read.** Each block carries an xxh3 bitrot hash.
  A damaged or missing shard is rebuilt from parity and repaired on read.
- **Writes are acknowledged after fsync.** Metadata is linearizable;
  `If-Match` and `If-None-Match` writes are atomic.
- **Secure by default.** Internode traffic is always mutual TLS. There are no
  default credentials: `unio init` generates them. Secrets are encrypted at
  rest, parsers are fuzzed, and SigV4 is checked as strictly as AWS does it.

## Quick start

```sh
# 1. Generate the cluster secret and root credentials into a 0600 file.
docker run --rm -u "$(id -u)" -v "$PWD:/w" ghcr.io/eukalpia/unio:0.1 init --output /w/unio.env

# 2. Start one node with four drives (Docker volumes here; use real disks in production).
docker run -d --name unio -p 9000:9000 --env-file unio.env \
  -e UNIO_ENDPOINTS='https://localhost:9100/data{1...4}' \
  -e UNIO_NODE_ID=localhost \
  -e UNIO_REQUIRE_TLS=false \
  -v unio1:/data1 -v unio2:/data2 -v unio3:/data3 -v unio4:/data4 \
  ghcr.io/eukalpia/unio:0.1

# 3. Use any S3 client. The access key and secret are UNIO_ROOT_USER and UNIO_ROOT_PASSWORD in unio.env.
aws --endpoint-url http://localhost:9000 s3 mb s3://demo
aws --endpoint-url http://localhost:9000 s3 cp ./file.pdf s3://demo/
```

`UNIO_REQUIRE_TLS=false` lets the S3 port serve plain HTTP for this local
test. In production, set `UNIO_TLS_CERT_FILE` and `UNIO_TLS_KEY_FILE`, or put
the node behind a TLS proxy.

Binaries for Linux and macOS (amd64 and arm64) are on the
[releases page](https://github.com/eukalpia/unio/releases). A multi-node
cluster lists every node's endpoints in `UNIO_ENDPOINTS`; see
[deploy/compose/cluster-4x4.yml](deploy/compose/cluster-4x4.yml) and
[docs/deploy/kubernetes.md](docs/deploy/kubernetes.md).

## Moving from MinIO

`unio-mirror` ships with every release. It copies buckets, objects, their
headers and metadata, and bucket policies from any S3 store, verifies every
object, and never deletes anything. A second run copies only what changed, so
the old store keeps serving while you copy.

```sh
export UNIO_MIRROR_FROM_ACCESS_KEY=... UNIO_MIRROR_FROM_SECRET_KEY=...   # MinIO
export UNIO_MIRROR_TO_ACCESS_KEY=...   UNIO_MIRROR_TO_SECRET_KEY=...     # Unio
unio-mirror -from http://minio:9000 -to http://unio:9000
unio-mirror -from http://minio:9000 -to http://unio:9000 -verify
```

The whole procedure, including a switch with no lost writes, is in
[docs/migrate-from-minio.md](docs/migrate-from-minio.md).

## What works

- Buckets, objects, multipart upload, copy, ranges, conditional requests,
  presigned URLs, object tagging, bucket policies (anonymous read and the
  common statements), IAM users and access keys.
- SigV4 in headers, in query strings and with chunked and trailing payloads.
- Web console at `/unio/console`, Prometheus metrics, health endpoints.

Not yet: versioning API, lifecycle rules, replication, server-side
encryption, object lock, event notifications, STS/OIDC/LDAP. The order is in
[ROADMAP.md](ROADMAP.md).

## How it is built

```
S3 clients ──► s3api (Hertz) ──► auth (SigV4/IAM)
                    │
                 object layer ──► meta (Multi-Raft + Pebble)
                    │
                 erasure (Reed-Solomon + bitrot) ──► storage (segments + blobs)
                    │
                 cluster (UniWire: mTLS RPC, shard streams, Raft)
```

Pure Go without CGO, built on
[CloudWeGo Hertz](https://github.com/cloudwego/hertz),
[hashicorp/raft](https://github.com/hashicorp/raft),
[cockroachdb/pebble](https://github.com/cockroachdb/pebble),
[klauspost/reedsolomon](https://github.com/klauspost/reedsolomon) and
[xxh3](https://github.com/zeebo/xxh3). The design is in
[docs/design/architecture.md](docs/design/architecture.md), and the decisions
behind it in [docs/adr](docs/adr/README.md).

## Contributing

Issues, benchmarks on your hardware and pull requests are welcome. Read
[CONTRIBUTING.md](CONTRIBUTING.md) for the setup and the quality gates, and
follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report security issues
privately as described in [SECURITY.md](SECURITY.md).

## License

Unio is licensed under the [Apache License, Version 2.0](LICENSE).

Unio is an independent project and is not affiliated with or endorsed by
MinIO, Inc. or Amazon Web Services. "S3" and "Amazon S3" are trademarks of
Amazon.com, Inc. or its affiliates.

# Roadmap

The roadmap is tracked with GitHub milestones and issues. Dates are
intentionally omitted until the project has a release history.

## v0.1 — Foundation

The goal of v0.1 is a working, secure multi-node cluster with the core S3 API.

| Workstream | Package | Scope |
|---|---|---|
| Contracts | `internal/core`, `api/proto`, `bufpool`, `hashreader` | Interfaces, shared types, protobuf schema, in-memory fakes, conformance suites |
| S3 API gateway | `internal/s3api` | Hertz server, routing, fast XML, S3 error model, limits |
| Authentication | `internal/auth` | SigV4 (header, presigned, chunked, trailers), IAM keys, basic policies |
| Erasure coding | `internal/erasure` | Streaming encode and decode, bitrot framing, quorums, heal-on-read |
| Drive engine | `internal/storage` | Packed segments, blob files, local index, group commit, compaction |
| Metadata | `internal/meta` | Multi-Raft on Pebble, state machines, key schema, listing |
| Cluster | `internal/cluster` | UniWire mTLS transport, PKI bootstrap, remote drives, Raft transport, topology |
| Object layer | `internal/object` | PUT/GET/DELETE/COPY/multipart orchestration, inline and packed tiers, GC queue |
| Platform | `cmd/unio`, `config`, `obs`, `test`, `bench`, `deploy` | CLI, configuration, observability, Docker Compose, integration, chaos and benchmark harnesses, CI |
| Integration | `internal/server` | Node assembly, end-to-end tests, `ceph/s3-tests` subset |

Exit criteria:

- all five correctness invariants are covered by passing tests;
- the `ceph/s3-tests` subset passes at 90% or more;
- CI is green, including race, fuzz smoke, vulnerability and lint checks.

## v0.2 — Performance

- Benchmark harness against MinIO on identical hardware (`warp` and
  project benchmarks).
- Profiling and tuning to the targets in the
  [architecture](docs/design/architecture.md#11-targets).
- Benchmark Hertz on the standard transport against `net/http`.
- Benchmark regression gates in CI.
- Official multi-arch container images with signatures and SBOMs.
- Helm chart for Kubernetes.
- Ecosystem compatibility matrix in CI: Velero, Loki, Thanos, Mimir, Tempo,
  Harbor, GitLab, restic, rclone, Kopia, Spark, Trino and Iceberg, AWS CLI
  and SDKs.

## v0.3 — Data management

- Versioning API.
- Lifecycle rules.
- Full bucket policies.
- Embedded web console (React, TypeScript, Tailwind CSS) with a fast object
  browser, upload manager and visual policy editor.
- `unioctl`: admin CLI and a Bubble Tea terminal UI with a live cluster
  dashboard and keyboard-driven bucket browser.
- Metadata range splits for very large buckets.

## v0.4 — Enterprise features

- Site and bucket replication.
- Server-side encryption (SSE-S3, SSE-C, KMS).
- Object lock and retention.
- Event notifications.
- STS, OIDC and LDAP.
- Online expansion and rebalancing.
- Kubernetes operator (`UnioCluster` custom resource) for day-2 operations.

## Later

- kTLS and io_uring data paths.
- S3 Select.
- Tiering.

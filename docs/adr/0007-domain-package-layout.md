# 0007. Domain package layout

- Status: Accepted
- Date: 2026-10-22

## Context

The first draft placed every shared type and interface in a single `core`
package. A catch-all package grows without bound, couples unrelated domains,
and makes ownership unclear: every change to it touches every module.

## Decision

Contracts are split by domain, following the standard library pattern of
`io/fs` (contract), `io/fs/fstest` (test helpers) and separate
implementations:

- Leaf packages `ids` and `s3err` hold identifiers and the S3 error model.
- Each domain package (`object`, `drive`, `erasure`, `meta`, `auth`,
  `cluster`) defines its interfaces, the types they exchange and its errors.
- Implementations live in subpackages (`drive/local`, `erasure/rs`,
  `meta/raftstore`, `auth/sigv4`, `cluster/uniwire`, `object/engine`, ...).
  They depend on contracts only, never on each other.
- Fakes and conformance suites live in `*test` subpackages
  (`drivetest`, `erasuretest`, `metatest`, `objecttest`, ...).
- Only `internal/server` imports implementations and wires them.
- Sentinel errors are created with `s3err.NewSentinel` and carry their S3
  code, so error classification needs no knowledge of the domains.
- Names avoid stutter: `object.Layer`, `erasure.Layout`, `meta.Store`,
  `cluster.Map`.

## Consequences

- Clear ownership per directory and smaller review surfaces.
- The dependency graph is acyclic by construction:
  `ids`, `s3err` -> `drive` -> `erasure` -> `object` -> `meta` -> `cluster`.
- Cross-domain types require an explicit import, which makes coupling
  visible in review.

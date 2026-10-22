# 0006. Protobuf and vtprotobuf for metadata and RPC

- Status: Accepted
- Date: 2026-10-22

## Context

Metadata records, Raft commands and RPC messages need:

- schema evolution across versions during rolling upgrades;
- fast, allocation-light encoding.

## Decision

Schemas live in `api/proto/unio/v1`. Code is generated with
`protoc-gen-go` and `protoc-gen-go-vtproto`, and committed under
`internal/pb`.

## Consequences

- Standard evolution rules: field numbers are never reused, and new fields
  are optional.
- Reflection-free marshalling on hot paths.
- Schema changes are contract changes and require maintainer review.

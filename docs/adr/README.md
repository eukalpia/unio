# Architecture Decision Records

We record significant architectural decisions as ADRs, following
[Michael Nygard's format](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions).

To propose a decision:

1. Copy [template.md](template.md) to `NNNN-short-title.md`, using the next
   free number.
2. Open a pull request. The ADR is accepted per [GOVERNANCE.md](../../GOVERNANCE.md).
3. Accepted ADRs are never edited in substance. Supersede them with a new ADR.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | Accepted |
| [0002](0002-metadata-plane.md) | Separate metadata plane on Multi-Raft and Pebble | Accepted |
| [0003](0003-uniwire-internode-transport.md) | UniWire: mTLS-only internode transport | Accepted |
| [0004](0004-hertz-standard-transport.md) | Hertz on the standard transport for the S3 API | Accepted |
| [0005](0005-size-tiered-layout.md) | Size-tiered data layout with logical shard ids | Accepted |
| [0006](0006-protobuf-encoding.md) | Protobuf and vtprotobuf for metadata and RPC | Accepted |
| [0007](0007-domain-package-layout.md) | Domain package layout | Accepted |

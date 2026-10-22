# 0003. UniWire: mTLS-only internode transport

- Status: Accepted
- Date: 2026-10-22

## Context

Internode traffic carries user data, metadata and credentials, so it must be
encrypted and mutually authenticated by default.

- CloudWeGo Kitex was the initial candidate. Its FAQ states that Kitex does
  not natively support TLS.
- Netpoll is optimised for many small RPCs, not for multi-gigabyte shard
  streams.

## Decision

Unio uses its own protocol, UniWire, over `crypto/tls` (TLS 1.3, mTLS) on a
single port.

- A connection preamble selects RPC (multiplexed protobuf requests), Stream
  (one shard stream per pooled connection) or Raft (an implementation of
  `raft.Transport`).
- Trust is bootstrapped from `UNIO_CLUSTER_SECRET`: an Ed25519 cluster CA
  key is derived via HKDF, and every node issues itself a certificate from
  it. Operator-provided certificates are also supported.
- Plaintext is possible only with `--insecure-internode`, which is intended
  for benchmarks.

## Consequences

- Secure by default with one secret to configure.
- We own and must fuzz the framing code. A one-stream-per-connection design
  for bulk data avoids building HTTP/2-style flow control.
- kTLS can later enable sendfile on Linux.

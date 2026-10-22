# 0004. Hertz on the standard transport for the S3 API

- Status: Accepted
- Date: 2026-10-22

## Context

The project uses the CloudWeGo stack where it helps. According to Hertz's
documentation:

- TLS is supported only on the standard (Go `net`) transport.
- Body streaming on netpoll is not true streaming: bodies are buffered in
  memory, which is unacceptable for multi-gigabyte uploads.

## Decision

The S3 API is served by Hertz on the standard transport with request-body
streaming enabled.

## Consequences

- TLS and bounded-memory uploads work.
- Hertz's netpoll performance advantage does not apply. In v0.2 we
  benchmark against `net/http` and revisit this decision if Hertz loses.

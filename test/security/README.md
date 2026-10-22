# Security attack suite

A black-box suite that attacks a running S3 endpoint and checks that every
attack is rejected without side effects and without leaking internals. It
knows nothing about Unio's code, so it is validated against another S3
implementation before it is used to judge Unio.

## What it covers

| Area | Attacks |
|---|---|
| Authentication | unsigned requests, wrong secret, unknown key, clock skew both ways |
| Tampering | signed header, path, method, query or payload changed after signing |
| Presigned URLs | expired, expiry over 7 days, object swapped, method swapped, expiry extended |
| Streaming uploads | aws-chunked chunk flipped, chunks reordered, final chunk dropped, decoded length lied about, declared trailer withheld |
| Integrity | Content-MD5 mismatch or malformed, checksum header mismatch, truncated body on a closed connection |
| Input handling | traversal-looking keys round-trip opaquely, oversized keys, metadata and headers |
| Hostile XML | entity expansion, external entities, deep nesting, too many keys, oversized bodies |
| Abuse (slow) | slow headers, stalled bodies, 2,000 idle connections |
| Leakage | stack traces, file paths, internal component names, secrets in error responses |

Every rejected write is followed by a check that the object did not appear.

## Running

```sh
export UNIO_TEST_ENDPOINT=http://localhost:9000
export UNIO_TEST_ACCESS_KEY=...
export UNIO_TEST_SECRET_KEY=...
go test -v ./test/security/

# Slow abuse tests (several minutes):
UNIO_TEST_SLOW=1 go test -v -timeout 15m ./test/security/
```

| Variable | Default | Meaning |
|---|---|---|
| `UNIO_TEST_REGION` | `us-east-1` | Signing region |
| `UNIO_TEST_FLAVOR` | `unio` | Set to another name to skip Unio-only guarantees |
| `UNIO_TEST_HEADER_TIMEOUT` | `30s` | Longest acceptable wait for incomplete headers |
| `UNIO_TEST_BODY_IDLE_TIMEOUT` | `60s` | Longest acceptable wait for a stalled body |

Unio-only guarantees go beyond what S3 requires: declared trailers must
arrive, the `unio` bucket name is reserved, and XML bodies are capped at
2 MiB.

## Validation

The suite was validated on 2026-10-22 against RustFS (`rustfs/rustfs:latest`)
with `UNIO_TEST_FLAVOR=rustfs`. MinIO images are no longer published. Passing
cases confirm that the attacks are well-formed; failing cases are behaviours
of the reference implementation that the suite correctly flags:

| Case | Reference result |
|---|---|
| Tampered aws-chunked chunk | rejected with 500 instead of 403 |
| Final aws-chunked chunk dropped | accepted (200) |
| User metadata over 2 KiB | accepted (200) |
| Malformed Content-MD5 | rejected with 500 instead of 400 |
| Slow headers | connection kept open over 60 s |
| Stalled body | connection kept open over 90 s |

All other cases passed. Unio must pass every case, including the Unio-only
ones.

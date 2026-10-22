# Threat Model

- Status: living document, reviewed with every security-relevant change
- Last updated: 2026-10-22

This document lists what Unio protects, who it protects it from, where the
trust boundaries are, and the mitigations every contribution must preserve.
Reviewers check security-relevant pull requests against it, using the
checklist at the end.

## 1. Assets

| Asset | Why it matters |
|---|---|
| Object data | Confidentiality and integrity of everything users store |
| Object metadata | Keys, sizes, user metadata and tags can be sensitive on their own |
| Credentials | Root credentials, user secret keys, the cluster secret and node keys |
| Availability | The service must stay up under malicious load |
| Audit trail | Access logs used for incident investigation |

## 2. Adversaries

| Adversary | Capabilities |
|---|---|
| **Anonymous internet client** | Sends arbitrary HTTP to the S3 port: malformed headers, huge or slow bodies, path tricks |
| **Authenticated user** | Holds valid keys with limited policies; tries to exceed them |
| **Network attacker** | Observes or modifies traffic between clients and nodes, or between nodes |
| **Compromised peer** | A host on the cluster network that is not a legitimate node |
| **Malicious or faulty drive** | Returns corrupted, truncated or stale data; fails slowly |
| **Supply chain** | A compromised dependency, build tool or release artifact |

Out of scope: an attacker with root on a cluster node or with the cluster
secret; physical access to drives (encryption at rest is planned for v0.4).

## 3. Trust boundaries

```
 untrusted                     │ trust boundary │            trusted after checks
───────────────────────────────┼────────────────┼─────────────────────────────────
 S3 clients ── HTTP(S) :9000 ──▶ s3api: limits, routing, SigV4, payload verification
 peers      ── UniWire :9100 ──▶ mTLS with cluster CA, frame limits, method allow-list
 drives     ── filesystem    ──▶ record checksums (drive), bitrot hashes (erasure)
 config/env ── operator      ──▶ validation, secret strength, redaction
```

Everything that crosses a boundary is parsed by code that is size-bounded,
fuzzed and never trusts lengths, counts or names from the other side.

## 4. Threats and mitigations

### 4.1 S3 front end

| Threat | Mitigation | Where |
|---|---|---|
| Signature bypass or forgery | Strict SigV4 parsing; canonical request per AWS rules; `hmac.Equal`; ±15 min skew; presigned expiry ≤ 7 days | `auth/sigv4` |
| Tampered streaming body | Every aws-chunked chunk signature verified before its bytes are released; trailer signatures verified | `auth/sigv4` |
| Corrupted upload committed | `object.Body` returns `io.EOF` only after length, Content-MD5, SHA-256, checksum headers and **declared** trailers verify; the object layer commits only after a clean EOF (I4) | `s3api/payload`, `object/engine` |
| Privilege escalation | Every operation maps to an IAM action and is authorized before touching storage; explicit deny wins; default deny | `s3api`, `auth/policy` |
| Resource exhaustion | Header, body, idle and write-idle timeouts; concurrency limit with bounded queue (`503 SlowDown`); bounded XML bodies; global buffer budget | `s3api/middleware`, `bufpool` |
| Memory amplification | No allocation sized from client-supplied numbers without a cap; streaming everywhere except bounded XML | all parsers |
| Information disclosure | Clients see only S3 codes and fixed messages; internal causes are logged with a request id | `s3err`, `s3api/apierr` |
| Reserved namespace abuse | `/unio/*` is never treated as a bucket; bucket name `unio` is rejected | `s3api/router` |
| Log injection or secret leakage | Structured logging; signatures, secrets and query strings are never logged | `obs`, `s3api/middleware` |

### 4.2 Identity and access

| Threat | Mitigation | Where |
|---|---|---|
| Default or weak credentials | No defaults; root credentials must meet length rules; `unio init` generates strong secrets | `config`, `auth/iam` |
| Secret theft from metadata | User secrets encrypted with AES-256-GCM under a key derived from the cluster secret (HKDF); access key id as additional data | `auth/iam` |
| Policy parser abuse | Size limit, strict JSON, unsupported elements rejected rather than ignored, linear-time wildcard matching | `auth/policy` |

### 4.3 Internode

| Threat | Mitigation | Where |
|---|---|---|
| Eavesdropping or tampering | TLS 1.3 on every internode connection; plaintext only with an explicit, loudly logged flag | `cluster/uniwire` |
| Rogue node joins | Mutual TLS with certificates issued by the cluster CA; node identity taken from the certificate, not from the payload | `cluster/pki` |
| Malformed frames | Fixed preamble, maximum frame size, length checks before allocation, fuzzed decoders | `cluster/uniwire` |
| Error-channel leaks | Only S3 codes and registered sentinels cross the wire; other errors become generic | `cluster/uniwire` |

### 4.4 Storage

| Threat | Mitigation | Where |
|---|---|---|
| Path traversal | On-disk names are hex ids only; user keys never reach the filesystem | `drive/local` |
| Silent corruption | Record header CRC32C on drives; xxh3-128 per shard block verified on every read; reconstruction from parity (I2) | `drive/local`, `erasure` |
| Torn writes and crashes | fsync before acknowledgement; atomic renames with directory fsync; torn segment tails truncated on recovery (I1) | `drive/local` |
| Lost updates | Linearizable metadata; old data deleted only after the new version commits (I5) | `meta`, `object/engine` |

### 4.5 Supply chain and release

| Threat | Mitigation |
|---|---|
| Vulnerable dependency | `govulncheck` and Dependabot security updates; approved dependency list |
| Malicious code in a pull request | Required review, CODEOWNERS for sensitive paths, CodeQL, gosec, staticcheck |
| Leaked secrets in the repository | Secret scanning with push protection |
| Tampered release artifacts | Reproducible `-trimpath` builds, no CGO, signed images with SBOMs (v0.2) |

## 5. Security review checklist

Every pull request that touches a trust boundary answers each item:

- [ ] Every input from across a boundary is length-limited before it is read
      or allocated.
- [ ] Parsers of untrusted input have fuzz targets, and the fuzzers ran.
- [ ] No secret, signature or object data can reach logs, metrics or errors.
- [ ] Secrets and signatures are compared in constant time.
- [ ] Authorization happens before any storage access.
- [ ] Nothing derived from user input reaches a filesystem path, a shell or a
      query language.
- [ ] Data is committed only after integrity verification completed.
- [ ] Timeouts and concurrency limits bound every network-facing operation.
- [ ] New goroutines have an owner, a cancellation path and a bound.
- [ ] Errors returned to clients carry only an S3 code and a fixed message.
- [ ] New dependencies are justified and pass `govulncheck`.

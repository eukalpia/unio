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
| Resource exhaustion | Header, read-idle, write-idle and keep-alive idle timeouts; concurrency limit with bounded queue (`503 SlowDown`); bounded XML bodies; global buffer budget | `s3api/middleware`, `bufpool` |
| Memory amplification | No allocation sized from client-supplied numbers without a cap; streaming everywhere except bounded XML | all parsers |
| Information disclosure | Clients see only S3 codes and fixed messages; internal causes are logged with a request id | `s3err`, `s3api/apierr` |
| Reserved namespace abuse | `/unio/*` is never treated as a bucket; bucket name `unio` is rejected | `s3api/router` |
| Log injection or secret leakage | Structured logging; signatures, secrets and query strings are never logged | `obs`, `s3api/middleware` |

### 4.2 Identity and access

| Threat | Mitigation | Where |
|---|---|---|
| Default or weak credentials | No defaults; root credentials must pass `auth.ValidateRootCredentials`, the same check at configuration load and at start; `unio init` generates strong secrets | `config`, `auth` |
| Secret theft from metadata | User secrets encrypted with AES-256-GCM under a key derived from the cluster secret (HKDF); access key id as additional data | `auth/iam` |
| Policy parser abuse | Size limit, strict JSON, unsupported elements rejected rather than ignored, linear-time wildcard matching | `auth/policy` |
| Privilege escalation through several policies | A user's access is the union of its inline policy and its attached managed policies. An explicit Deny in any matching statement of any of them wins, an Allow must match otherwise, and anything else is denied. One function, `policy.EvaluateAll`, implements the rule, and it is linear in the total number of statements | `auth/policy`, `auth/iam` |
| Attachment outliving its policy (dangling grant) | A policy is marked as deleting before its attachments are counted. An attach writes its attachment first, then confirms that the policy is live, and rolls the attachment back when it is not. Evaluation ignores an attachment unless its policy is live and has the same incarnation id, so a rollback that failed still grants nothing | `auth/iam` |
| Access kept after a policy change | Replacing a document, detaching and deleting clear the local cache at once. Other nodes see the change within the cache TTL, 5 seconds by default. Deleting a policy that is still attached fails with `DeleteConflict` | `auth/iam` |
| Inheritance by a re-created name | Each managed policy has a random incarnation id, and each attachment records it. A user's attachments are stored in the user record, so they are removed with the user. The incarnation-scoped deletion removes only the records of the deleted user or policy, and a delete that overlaps a re-creation leaves the new record alone | `auth/iam` |
| Unbounded policy state | At most 1000 managed policies, counted by a slot counter that is never below the number of records; at most 10 attached policies per user; document size limit as for inline policies | `auth/iam` |
| Policies on the root identity | Root keeps its implicit full access and cannot have policies. Attach, detach and inline policy changes for root are refused with `InvalidArgument` | `auth/iam` |

Known limit: a storage failure during the rollback of a refused attach leaves
the attachment in place. It then takes effect as soon as its policy is live.
The attach reports the failure, so an operator must list the user's policies
after such an error, and detach any attachment that should not exist.

### 4.3 Internode

| Threat | Mitigation | Where |
|---|---|---|
| Eavesdropping or tampering | TLS 1.3 on every internode connection; no setting turns it off, and configuration refuses `http` endpoints | `cluster/uniwire`, `config` |
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

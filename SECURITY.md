# Security Policy

## Supported versions

Unio is pre-1.0. Security fixes go to `main` and to the latest released
minor version.

## Reporting a vulnerability

**Do not open public issues for security problems.**

Report vulnerabilities privately through
[GitHub Security Advisories](https://github.com/eukalpia/unio/security/advisories/new).

Please include:

- affected version or commit;
- a description of the issue and its impact;
- steps to reproduce or a proof of concept;
- any suggested mitigation.

What to expect:

- We acknowledge reports within **3 business days**.
- We provide an initial assessment within **10 business days**.
- We coordinate disclosure with the reporter. Fixes are released before
  details are made public, and reporters are credited unless they prefer
  otherwise.

## Threat model

What Unio protects and how is described in
[docs/security/threat-model.md](docs/security/threat-model.md).

## Scope

In scope:

- authentication and authorisation bypass (SigV4, IAM, policies);
- data integrity violations: serving corrupted data, losing acknowledged
  writes;
- internode transport weaknesses (UniWire, mTLS, PKI bootstrap);
- remote crashes, resource exhaustion or memory disclosure triggered by
  crafted requests;
- secret exposure in logs, metrics or errors.

Out of scope: plain-HTTP S3 endpoints exposed to untrusted networks against
documented guidance.

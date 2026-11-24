# 0008. Bucket policies

- Status: Accepted
- Date: 2026-11-24

## Context

Web applications keep most of their files private and publish a few
prefixes: logos, public documents, profile photos. With S3 this is a bucket
policy that allows `s3:GetObject` to everyone on those prefixes. Without it,
every public file has to go through a presigned URL or a proxy, and the
first application moving to Unio (an LMS with a public admission site) could
not move at all.

A bucket policy is also the most common source of data leaks in S3: a
principal or resource broader than intended opens a whole bucket. Whatever
Unio accepts must mean exactly what it appears to mean.

## Decision

- The policy is stored as the document the client sent, in a new optional
  field `BucketRecord.policy` (field 7), and changed by a new Raft command
  `SetBucketPolicy` (26) in the system group. Data partitions never consult
  it, so it is not copied to them. The state machine checks only the size
  (20 KiB); parsing happens before the proposal, so a stricter parser in a
  later release cannot make replaying an old log diverge.
- `policy.ParseBucket` is the identity-policy parser with three changes:
  `Principal` is required and only `"*"` (everyone) is accepted; every
  `Resource` must be the bucket's ARN or lie under it; every `Action` must be
  an `s3:` action, and a bare `"*"` means `s3:*`. Anything else, including
  named principals and `Condition`, is refused with `MalformedPolicy`
  instead of being ignored.
- Authorization (`package bucketpolicy`): root is allowed; a Deny in the
  bucket policy refuses everyone else; a Deny from identity policies, or an
  unknown or disabled user, refuses; an Allow from either side allows;
  anything else is refused. An anonymous request can only be allowed by an
  explicit statement of the bucket policy.
- Each node caches the parsed policy for one second. A change made on a
  node applies there at once; on other nodes within one second. A stored
  document that no longer parses is logged and treated as absent, so
  anonymous access fails closed.
- `GET`, `PUT` and `DELETE /bucket?policy` are implemented; `?policy` on an
  object stays `NotImplemented`.

## Consequences

- Public prefixes work with standard tools (AWS SDKs, `mc anonymous
  set-json`), and the LMS policy moves over unchanged.
- Named principals and conditions are not available yet. Adding them later
  widens what is accepted without changing the meaning of a stored policy.
- A revoked public read can still be served by another node for up to one
  second.
- The stored record format grows by one optional field; older records read
  as "no policy".

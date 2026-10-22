// Package iam manages users, access keys and identity policies on top of
// meta.SystemKV, and implements auth.IAM. The compile-time assertion in
// store.go keeps *Store in step with that interface.
//
// Layout under the "iam/" prefix:
//
//	iam/users/<name>      user record: id, name, creation time, enabled flag,
//	                      deleting flag, and the managed policies attached
//	iam/keys/<accessKey>  access key record: owner, owner id, sealed secret
//	iam/policies/<name>   inline policy record: owner id and the policy document
//	iam/managed/<name>    managed policy record: incarnation id, the policy
//	                      document, deleting flag
//	iam/managed-count     count of managed policy slots in use
//
// Every record is compact JSON with a version field. Secrets are sealed with
// AES-256-GCM under a key-encryption key derived from the cluster secret
// (DeriveKEK). The access key ID is the associated data, so a sealed secret
// cannot be moved to another key.
//
// Records carry the owner's random id: a user id on users, keys and inline
// policies, and an incarnation id on managed policies, which attachments also
// record. A key, policy or attachment written for an earlier user or policy of
// the same name therefore never applies to a new one, even when a delete raced
// with a write.
//
// Access is decided by one rule over the inline policy and the live attached
// managed policies together, in policy.EvaluateAll. See the doc comment on
// auth.IAM.
//
// Writes use compare-and-swap with bounded retries.
//
// Revocation window. Keys, users and policies are read through an in-memory
// cache with a TTL. The default TTL is 5 seconds (Config.CacheTTL). A change
// made on this node, such as deleting a key, disabling a user, replacing a
// managed policy, or detaching one, takes effect at once because the local
// cache is cleared. A change made on another node takes effect on this node
// within CacheTTL. So a revoked key or a disabled user can still authenticate
// or be authorized on a peer for up to that long. Lower CacheTTL to shorten
// the window, at the cost of more storage reads.
package iam

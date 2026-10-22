// Package iam manages users, access keys and identity policies on top of
// meta.SystemKV, and implements auth.IAM.
//
// Layout under the "iam/" prefix:
//
//	iam/users/<name>      user record: id, name, creation time, enabled flag
//	iam/keys/<accessKey>  access key record: owner, owner id, sealed secret
//	iam/policies/<name>   policy record: owner id and the policy document
//
// Every record is compact JSON with a version field. Secrets are sealed with
// AES-256-GCM under a key-encryption key derived from the cluster secret
// (DeriveKEK). The access key ID is the associated data, so a sealed secret
// cannot be moved to another key.
//
// Records carry the owner's random user id. A key or policy written for an
// earlier user of the same name therefore never applies to a new user, even
// when a delete raced with a write.
//
// Writes use compare-and-swap with bounded retries. Reads are cached for a
// short TTL, so a change made on another node takes effect within CacheTTL.
// Changes made on this node are visible immediately.
package iam

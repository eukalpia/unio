package meta

import "context"

// AnyRevision makes SystemKV writes unconditional.
const AnyRevision = ^uint64(0)

// MaxSystemValueSize bounds SystemKV values.
const MaxSystemValueSize = 1 << 20

// KV is a key, value and revision from SystemKV.
type KV struct {
	Key      string
	Value    []byte
	Revision uint64
}

// SystemKV is a small linearizable key-value store in the system group,
// used for IAM, bucket configuration and cluster state.
type SystemKV interface {
	// Get fails with ErrNotFound.
	Get(ctx context.Context, key string) (value []byte, revision uint64, err error)
	// Put writes value if the key's current revision equals expectRevision
	// (0 means the key must not exist; AnyRevision skips the check) and
	// returns the new revision. Fails with ErrRevisionMismatch.
	Put(ctx context.Context, key string, value []byte, expectRevision uint64) (uint64, error)
	// Delete removes key under the same revision rules. Deleting a missing
	// key with AnyRevision or with expectRevision 0 succeeds.
	Delete(ctx context.Context, key string, expectRevision uint64) error
	// List returns all entries whose key starts with prefix, in key order.
	List(ctx context.Context, prefix string) ([]KV, error)
}

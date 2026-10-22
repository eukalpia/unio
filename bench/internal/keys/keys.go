// Package keys generates the object keys used by the benchmark workloads. Key
// generation is deterministic, so a given seed always selects the same keys.
package keys

import (
	"fmt"
	"math/rand/v2"
)

// listPrefixes is the default number of top-level prefixes for the LIST
// layout.
const listPrefixes = 4

// archiveEvery places every n-th key of a prefix under an archive/ sub-prefix,
// so a delimiter listing returns one common prefix alongside the objects.
const archiveEvery = 10

// Keyspace is a fixed set of n keys, prefix + zero-padded index, shared by the
// workloads of one object size.
type Keyspace struct {
	prefix string
	n      int
}

// NewKeyspace returns a keyspace of n keys under prefix. n must be at least 1.
func NewKeyspace(prefix string, n int) (Keyspace, error) {
	if n < 1 {
		return Keyspace{}, fmt.Errorf("keyspace %q needs at least 1 key, got %d", prefix, n)
	}
	return Keyspace{prefix: prefix, n: n}, nil
}

// Len returns the number of keys in the keyspace.
func (k Keyspace) Len() int {
	return k.n
}

// Key returns the key at index i. Indices outside [0, Len) wrap around, so
// Key never fails.
func (k Keyspace) Key(i int) string {
	i = ((i % k.n) + k.n) % k.n
	return fmt.Sprintf("%s%08d", k.prefix, i)
}

// Pick returns a key chosen uniformly at random from rng.
func (k Keyspace) Pick(rng *rand.Rand) string {
	return k.Key(rng.IntN(k.n))
}

// ListLayout describes the objects pre-filled for the LIST workload. Object i
// lives under prefix i mod Prefixes. Within a prefix, every archiveEvery-th
// object is placed under an archive/ sub-prefix.
type ListLayout struct {
	// Keys is the total number of objects.
	Keys int
	// Prefixes is the number of top-level prefixes.
	Prefixes int
}

// NewListLayout returns the layout for keys objects. keys must be at least 1.
func NewListLayout(keys int) (ListLayout, error) {
	if keys < 1 {
		return ListLayout{}, fmt.Errorf("list layout needs at least 1 key, got %d", keys)
	}
	return ListLayout{Keys: keys, Prefixes: min(listPrefixes, keys)}, nil
}

// Prefix returns the top-level prefix p, for example "list/p01/".
func (l ListLayout) Prefix(p int) string {
	return fmt.Sprintf("list/p%02d/", p)
}

// Key returns the key of object i, for 0 <= i < Keys.
func (l ListLayout) Key(i int) string {
	p := i % l.Prefixes
	within := i / l.Prefixes
	if within%archiveEvery == archiveEvery-1 {
		return fmt.Sprintf("%sarchive/obj-%010d", l.Prefix(p), i)
	}
	return fmt.Sprintf("%sobj-%010d", l.Prefix(p), i)
}

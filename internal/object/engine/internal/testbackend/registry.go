package testbackend

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// maxSets bounds the sets of a registry. The placement hash is reduced modulo
// the set count in 32 bits, so the count must fit in a uint32.
const maxSets = 1 << 16

// Registry is an in-memory erasure.Registry over a fixed list of Sets.
type Registry struct {
	sets []*Set
	byID map[ids.SetID]*Set
}

var _ erasure.Registry = (*Registry)(nil)

// NewRegistry returns a registry of n sets, each with k data and m parity
// shards. It panics on a count no registry can have; that is a programming error
// in the test that builds the registry.
func NewRegistry(n, k, m int) *Registry {
	if n < 1 || n > maxSets {
		panic(fmt.Sprintf("testbackend: set count %d outside [1, %d]", n, maxSets))
	}
	r := &Registry{byID: make(map[ids.SetID]*Set, n)}
	for i := 0; i < n; i++ {
		// i < n <= maxSets, checked above, so the id fits in a uint32.
		s := NewSet(ids.SetID(i+1), k, m) // #nosec G115 -- i is below maxSets, checked above
		r.sets = append(r.sets, s)
		r.byID[s.ID()] = s
	}
	return r
}

// Place implements erasure.Registry. Sets are chosen by hashing the key, so
// a key always lands on the same set.
func (r *Registry) Place(_ context.Context, bucket, key string, _ int64) (erasure.Set, error) {
	count := len(r.sets)
	if count == 0 || count > maxSets {
		return nil, fmt.Errorf("testbackend: no erasure sets: %w", erasure.ErrUnknownSet)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(bucket)) // hash.Hash writes never fail
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key))
	// count is in [1, maxSets] here, so it fits in a uint32.
	slot := h.Sum32() % uint32(count) // #nosec G115 -- count is checked above
	return r.sets[slot], nil
}

// Get implements erasure.Registry.
func (r *Registry) Get(id ids.SetID) (erasure.Set, error) {
	s, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("testbackend: set %d: %w", id, erasure.ErrUnknownSet)
	}
	return s, nil
}

// Set returns set i in registration order, for fault injection.
func (r *Registry) Set(i int) *Set { return r.sets[i] }

// FailWrites makes every drive in every set reject writes while on is true,
// so that no write reaches its quorum.
func (r *Registry) FailWrites(on bool) {
	for _, s := range r.sets {
		s.FailWrites(on)
	}
}

// StoredData returns the number of distinct data ids that still hold at
// least one shard copy. Tests use it to detect leaked or undeleted data.
func (r *Registry) StoredData() int {
	seen := make(map[ids.DataID]struct{})
	for _, s := range r.sets {
		s.dataIDs(seen)
	}
	return len(seen)
}

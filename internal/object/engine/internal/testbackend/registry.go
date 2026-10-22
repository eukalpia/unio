package testbackend

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// Registry is an in-memory erasure.Registry over a fixed list of Sets.
type Registry struct {
	sets []*Set
	byID map[ids.SetID]*Set
}

var _ erasure.Registry = (*Registry)(nil)

// NewRegistry returns a registry of n sets, each with k data and m parity
// shards.
func NewRegistry(n, k, m int) *Registry {
	r := &Registry{byID: make(map[ids.SetID]*Set, n)}
	for i := 0; i < n; i++ {
		s := NewSet(ids.SetID(i+1), k, m)
		r.sets = append(r.sets, s)
		r.byID[s.ID()] = s
	}
	return r
}

// Place implements erasure.Registry. Sets are chosen by hashing the key, so
// a key always lands on the same set.
func (r *Registry) Place(_ context.Context, bucket, key string, _ int64) (erasure.Set, error) {
	if len(r.sets) == 0 {
		return nil, fmt.Errorf("testbackend: no erasure sets: %w", erasure.ErrUnknownSet)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(bucket))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key))
	return r.sets[int(h.Sum32()%uint32(len(r.sets)))], nil
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

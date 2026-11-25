package placement

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/zeebo/xxh3"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// Place implements erasure.Registry. It picks the eligible set with the highest
// rendezvous weight for the key, so objects spread evenly over the sets. While
// capacity does not change, the same bucket and key always get the same set.
// When the set that wins for a key becomes ineligible, only the keys that set
// held move. Place returns ErrNoSpace when no set is eligible.
func (r *Registry) Place(ctx context.Context, bucket, key string, size int64) (erasure.Set, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("placement: place: %w", err)
	}
	if size < 0 {
		return nil, fmt.Errorf("placement: negative object size %d", size)
	}
	// A nil snapshot means no refresh has run yet, so every set is eligible.
	snap := r.capacity.Load()
	keyHash := xxh3.HashString(bucket + "\x00" + key)
	best := -1
	var bestWeight uint64
	for i, s := range r.sets {
		if snap != nil {
			k, _ := s.Shape()
			if !snap.sets[i].accepts(shardSize(size, k), r.fullRatio) {
				continue
			}
		}
		if w := weight(keyHash, s.ID()); best < 0 || w > bestWeight {
			best, bestWeight = i, w
		}
	}
	if best < 0 {
		return nil, ErrNoSpace
	}
	return r.sets[best], nil
}

// weight returns the rendezvous (highest random weight) score of set id for a
// key hash.
func weight(keyHash uint64, id ids.SetID) uint64 {
	var b [12]byte
	binary.BigEndian.PutUint64(b[:8], keyHash)
	binary.BigEndian.PutUint32(b[8:], uint32(id))
	return xxh3.Hash(b[:])
}

// shardSize returns the bytes that each drive of a set with k data shards
// stores for an object of size bytes: ceil(size/k). size must not be negative.
func shardSize(size int64, k int) uint64 {
	kk := int64(k)
	n := size / kk
	if size%kk != 0 {
		n++
	}
	// #nosec G115 -- Place rejects negative sizes, so n is not negative
	return uint64(n)
}

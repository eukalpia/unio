package erasure

import (
	"context"

	"github.com/eukalpia/unio/internal/ids"
)

// Registry resolves erasure sets for placement and reads. Implemented by the
// node assembly from the cluster topology.
type Registry interface {
	// Place picks the set for new data, skipping sets that are too full.
	Place(ctx context.Context, bucket, key string, size int64) (Set, error)
	// Get returns the set with the given id.
	Get(id ids.SetID) (Set, error)
}

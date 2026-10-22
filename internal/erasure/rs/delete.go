package rs

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// shardRef names one shard on the drive that holds it.
type shardRef struct {
	d  drive.Drive
	id ids.ShardID
}

// Delete implements erasure.Set. Every shard recorded in the layout is
// deleted in parallel. Missing shards are not errors; the other failures are
// joined into the returned error.
func (s *Set) Delete(ctx context.Context, id ids.DataID, layout erasure.Layout) error {
	if err := s.checkLayout(layout); err != nil {
		return err
	}
	var refs []shardRef
	var errs []error
	for i, want := range layout.Drives {
		if want.IsZero() {
			continue
		}
		d := s.shardDrive(layout, i)
		if d == nil {
			errs = append(errs, fmt.Errorf("erasure/rs: delete %s shard %d: drive %s is not in the set", id, i, want))
			continue
		}
		refs = append(refs, shardRef{d: d, id: shardID(id, i)})
	}
	errs = append(errs, deleteShards(ctx, refs)...)
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("erasure/rs: delete %s: %w", id, err)
	}
	return nil
}

// deleteShards deletes every ref in parallel. Missing shards are not errors.
// It returns one error per ref, in order, with nil for successes.
func deleteShards(ctx context.Context, refs []shardRef) []error {
	errs := make([]error, len(refs))
	var wg sync.WaitGroup
	for i, r := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := r.d.DeleteShard(ctx, r.id)
			if err != nil && !errors.Is(err, drive.ErrShardNotFound) {
				errs[i] = fmt.Errorf("shard %s on drive %s: %w", r.id, r.d.ID(), err)
			}
		}()
	}
	wg.Wait()
	return errs
}

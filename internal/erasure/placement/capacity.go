package placement

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/drive"
)

// infoTimeout bounds one Info call made by Refresh, so that one slow drive
// cannot stall the whole refresh. A drive that does not answer in time counts
// as offline for that refresh.
const infoTimeout = 2 * time.Second

// setCapacity is the free space of one set at one refresh.
type setCapacity struct {
	// online counts the drives that reported themselves online.
	online int
	// total and used sum the capacity of the online drives that report one.
	total, used uint64
	// minFree is the smallest free space of any online drive.
	minFree uint64
}

// accepts reports whether the set can take one more object whose shards are
// shard bytes each. A set with no online drive takes nothing. A set is full
// when its used fraction reaches fullRatio. Otherwise the shard must fit on the
// emptiest online drive.
func (c setCapacity) accepts(shard uint64, fullRatio float64) bool {
	if c.online == 0 {
		return false
	}
	if c.total > 0 && float64(c.used)/float64(c.total) >= fullRatio {
		return false
	}
	return shard <= c.minFree
}

// snapshot is the capacity of every set, indexed like Registry.sets. It is
// immutable once published.
type snapshot struct {
	sets []setCapacity
}

// Refresh collects drive.Info from every drive concurrently, each call bounded
// by infoTimeout, and publishes the result for Place. A drive that fails or
// does not answer in time counts as offline. If ctx is done before the
// collection finishes, Refresh publishes nothing, so the previous snapshot
// stays in force.
func (r *Registry) Refresh(ctx context.Context) {
	infos := make([][]drive.Info, len(r.drives))
	var wg sync.WaitGroup
	for i, drives := range r.drives {
		infos[i] = make([]drive.Info, len(drives))
		for j, d := range drives {
			wg.Add(1)
			go func() {
				defer wg.Done()
				infos[i][j] = r.info(ctx, d)
			}()
		}
	}
	wg.Wait()
	if ctx.Err() != nil {
		// Every call failed because the caller stopped, not because the
		// drives did. Keep the last good snapshot.
		return
	}
	snap := &snapshot{sets: make([]setCapacity, len(r.sets))}
	for i, group := range infos {
		c := setCapacity{minFree: math.MaxUint64}
		for _, info := range group {
			if !info.Online {
				continue
			}
			c.online++
			c.minFree = min(c.minFree, info.Free)
			if info.Total > 0 {
				c.total += info.Total
				c.used += info.Used
			}
		}
		snap.sets[i] = c
	}
	r.capacity.Store(snap)
}

// info returns the capacity report of d. A drive that errors or does not answer
// within infoTimeout reports as offline.
func (r *Registry) info(ctx context.Context, d drive.Drive) drive.Info {
	cctx, cancel := context.WithTimeout(ctx, infoTimeout)
	defer cancel()
	info, err := d.Info(cctx)
	if err != nil {
		r.logger.Debug("erasure drive did not report capacity", "drive", d.ID(), "err", err)
		return drive.Info{ID: d.ID()}
	}
	return info
}

// Run refreshes capacity immediately, then every RefreshInterval, until ctx is
// done. It returns after ctx is done.
func (r *Registry) Run(ctx context.Context) {
	r.Refresh(ctx)
	ticker := time.NewTicker(r.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Refresh(ctx)
		}
	}
}

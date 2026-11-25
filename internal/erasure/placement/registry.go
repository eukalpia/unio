// Package placement chooses the erasure set for new objects and resolves set
// ids to the sets that hold them. It builds one rs.Set for every erasure set of
// a cluster map, and keeps the free space of each set up to date with a
// periodic capacity refresh.
package placement

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/offline"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/rs"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/s3err"
)

// ErrNoSpace is returned by Place when no erasure set can take the object,
// because every set is full or too small for its shards.
var ErrNoSpace = s3err.NewSentinel("placement: no erasure set has room for the object", s3err.InsufficientStorage)

// DriveSource resolves a drive id to the drive that serves it, locally or over
// the cluster network.
type DriveSource interface {
	Drive(id ids.DriveID) (drive.Drive, error)
}

// Registry implements erasure.Registry over the erasure sets of a cluster map.
// It is safe for concurrent use.
type Registry struct {
	// sets are ordered by set id.
	sets []*rs.Set
	// drives[i] are the drives of sets[i], in slot order.
	drives    [][]drive.Drive
	byID      map[ids.SetID]int
	fullRatio float64
	// refreshInterval is how often Run refreshes capacity.
	refreshInterval time.Duration
	logger          *slog.Logger
	// capacity is the latest snapshot. It is nil until the first Refresh, when
	// every set is eligible.
	capacity atomic.Pointer[snapshot]
}

var _ erasure.Registry = (*Registry)(nil)

// New builds one rs.Set for every erasure set of m, in set-id order. Each slot
// takes its drive from src. A drive that src cannot provide, or provides as a
// different drive, is replaced by an offline placeholder, and the problem is
// logged once at Warn. All sets share the budget, stripe size, stall timeout
// and heal queue size of opts.
func New(m *cluster.Map, src DriveSource, opts Options) (*Registry, error) {
	switch {
	case m == nil:
		return nil, errors.New("placement: nil cluster map")
	case src == nil:
		return nil, errors.New("placement: nil drive source")
	case len(m.Sets) == 0:
		return nil, errors.New("placement: cluster map has no erasure sets")
	}
	opts, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	locations := make(map[ids.DriveID]cluster.DriveLocation, len(m.Drives))
	for _, loc := range m.Drives {
		locations[loc.ID] = loc
	}
	sets := slices.Clone(m.Sets)
	slices.SortFunc(sets, func(a, b cluster.SetInfo) int { return cmp.Compare(a.ID, b.ID) })

	r := &Registry{
		byID:            make(map[ids.SetID]int, len(sets)),
		fullRatio:       opts.FullRatio,
		refreshInterval: opts.RefreshInterval,
		logger:          opts.Logger,
	}
	rsOpts := rs.Options{
		Budget:        opts.Budget,
		StripeSize:    opts.StripeSize,
		StallTimeout:  opts.StallTimeout,
		HealQueueSize: opts.HealQueueSize,
	}
	for _, info := range sets {
		if _, dup := r.byID[info.ID]; dup {
			return nil, fmt.Errorf("placement: set %d appears twice in the cluster map", info.ID)
		}
		drives, err := resolveDrives(info, locations, src, opts.Logger)
		if err != nil {
			return nil, err
		}
		s, err := rs.New(info.ID, drives, info.DataShards, info.ParityShards, rsOpts)
		if err != nil {
			return nil, fmt.Errorf("placement: set %d: %w", info.ID, err)
		}
		r.byID[info.ID] = len(r.sets)
		r.sets = append(r.sets, s)
		r.drives = append(r.drives, drives)
	}
	return r, nil
}

// resolveDrives returns the drives of info in slot order. A drive that src
// cannot provide becomes an offline placeholder that keeps its id and its
// location from the map, and the error is logged once.
func resolveDrives(info cluster.SetInfo, locations map[ids.DriveID]cluster.DriveLocation,
	src DriveSource, logger *slog.Logger) ([]drive.Drive, error) {
	drives := make([]drive.Drive, len(info.Drives))
	for slot, id := range info.Drives {
		loc, ok := locations[id]
		if !ok {
			return nil, fmt.Errorf("placement: set %d: drive %s has no location in the cluster map", info.ID, id)
		}
		d, err := src.Drive(id)
		if err == nil && d == nil {
			err = errors.New("drive source returned no drive")
		}
		if err == nil && d.ID() != id {
			err = fmt.Errorf("drive source returned drive %s", d.ID())
		}
		if err != nil {
			logger.Warn("erasure drive unavailable, serving it as offline",
				"set", info.ID, "slot", slot, "drive", id, "node", loc.Node, "err", err)
			d = offline.New(id, loc.Node, loc.Path)
		}
		drives[slot] = d
	}
	return drives, nil
}

// Get implements erasure.Registry. It returns erasure.ErrUnknownSet for an id
// that is not in the cluster map.
func (r *Registry) Get(id ids.SetID) (erasure.Set, error) {
	i, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("placement: set %d: %w", id, erasure.ErrUnknownSet)
	}
	return r.sets[i], nil
}

// Sets returns the sets ordered by set id. The slice is a copy; the sets are
// shared with the registry.
func (r *Registry) Sets() []*rs.Set {
	return slices.Clone(r.sets)
}

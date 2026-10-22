// Package rs implements erasure.Set with systematic Reed-Solomon coding.
//
// An object is cut into stripes of Options.StripeSize data bytes. Each stripe
// is split into k data blocks and encoded into m parity blocks, and every
// block is framed with its bitrot digest (package bitrot). Drive i stores
// shard i of every object of the set.
package rs

import (
	"fmt"
	"sync"

	"github.com/klauspost/reedsolomon"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/ids"
)

// maxShards bounds the shards of one set. It matches the GF(2^8) field of the
// Reed-Solomon encoder and keeps every shard index well inside the uint16 of
// ids.ShardID.
const maxShards = 256

// Set is a Reed-Solomon erasure set over a fixed group of drives. It is safe
// for concurrent use.
type Set struct {
	id     ids.SetID
	drives []drive.Drive
	k, m   int
	opts   Options
	enc    reedsolomon.Encoder
	heal   *HealQueue
	// geoms caches the buffer pools per stripe size. Layouts record their
	// stripe size, so a set may read objects written with other sizes.
	geoms sync.Map
}

var _ erasure.Set = (*Set)(nil)

// New returns a set over drives, where drives[i] stores shard i. It fails if
// the shape is invalid, a drive is nil, or the budget cannot hold one stripe
// of buffers.
func New(id ids.SetID, drives []drive.Drive, dataShards, parityShards int, opts Options) (*Set, error) {
	switch {
	case dataShards < 1:
		return nil, fmt.Errorf("erasure/rs: data shards %d must be at least 1", dataShards)
	case parityShards < 0:
		return nil, fmt.Errorf("erasure/rs: parity shards %d must not be negative", parityShards)
	case dataShards > maxShards || parityShards > maxShards || dataShards+parityShards > maxShards:
		return nil, fmt.Errorf("erasure/rs: %d+%d shards exceed the limit of %d", dataShards, parityShards, maxShards)
	case len(drives) != dataShards+parityShards:
		return nil, fmt.Errorf("erasure/rs: %d drives for %d+%d shards", len(drives), dataShards, parityShards)
	}
	for i, d := range drives {
		if d == nil {
			return nil, fmt.Errorf("erasure/rs: drive %d is nil", i)
		}
	}
	opts, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	enc, err := reedsolomon.New(dataShards, parityShards,
		reedsolomon.WithAutoGoroutines(bitrot.ShardBlockSize(opts.StripeSize, dataShards)))
	if err != nil {
		return nil, fmt.Errorf("erasure/rs: encoder for %d+%d: %w", dataShards, parityShards, err)
	}
	s := &Set{
		id:     id,
		drives: append([]drive.Drive(nil), drives...),
		k:      dataShards,
		m:      parityShards,
		opts:   opts,
		enc:    enc,
		heal:   newHealQueue(opts.HealQueueSize),
	}
	if _, err := s.geomFor(opts.StripeSize); err != nil {
		return nil, err
	}
	return s, nil
}

// ID implements erasure.Set.
func (s *Set) ID() ids.SetID { return s.id }

// Shape implements erasure.Set.
func (s *Set) Shape() (dataShards, parityShards int) { return s.k, s.m }

// HealQueue returns the queue of shards that reads found damaged. Repair
// work consumes it and calls Heal.
func (s *Set) HealQueue() *HealQueue { return s.heal }

// geom describes how objects with one stripe size are laid out, and the
// buffer pools their stripes use.
type geom struct {
	stripe int           // data bytes per stripe
	block  int           // bytes per shard block: ceil(stripe/k)
	frames *bufpool.Pool // one frame: k+m blocks
	blocks *bufpool.Pool // one block
}

// geomFor returns the geometry for stripe size stripe, creating it on first
// use. It fails if one frame does not fit in the budget.
func (s *Set) geomFor(stripe int) (*geom, error) {
	if v, ok := s.geoms.Load(stripe); ok {
		return v.(*geom), nil
	}
	if stripe < 1 {
		return nil, fmt.Errorf("erasure/rs: stripe size %d must be positive", stripe)
	}
	block := bitrot.ShardBlockSize(stripe, s.k)
	frame := (s.k + s.m) * block
	if int64(frame) > s.opts.Budget.Total() {
		return nil, fmt.Errorf("erasure/rs: stripe of %d bytes needs %d bytes of buffers; budget is %d",
			stripe, frame, s.opts.Budget.Total())
	}
	g := &geom{
		stripe: stripe,
		block:  block,
		frames: bufpool.NewPool(frame, s.opts.Budget),
		blocks: bufpool.NewPool(block, s.opts.Budget),
	}
	v, _ := s.geoms.LoadOrStore(stripe, g)
	return v.(*geom), nil
}

// checkLayout reports whether layout was written by this set with its shape.
func (s *Set) checkLayout(layout erasure.Layout) error {
	switch {
	case layout.Set != s.id:
		return fmt.Errorf("erasure/rs: layout of set %d used with set %d: %w", layout.Set, s.id, erasure.ErrUnknownSet)
	case layout.DataShards != s.k || layout.ParityShards != s.m:
		return fmt.Errorf("erasure/rs: layout shape %d+%d does not match set shape %d+%d",
			layout.DataShards, layout.ParityShards, s.k, s.m)
	case len(layout.Drives) != s.k+s.m:
		return fmt.Errorf("erasure/rs: layout has %d drive entries for %d shards", len(layout.Drives), s.k+s.m)
	}
	return nil
}

// layoutGeom validates layout against the set and returns its geometry.
func (s *Set) layoutGeom(layout erasure.Layout) (*geom, error) {
	if err := s.checkLayout(layout); err != nil {
		return nil, err
	}
	return s.geomFor(layout.StripeSize)
}

// shardDrive returns the drive that layout records for shard i, or nil when
// the shard was not written or its drive is not in this set. Shard ids do
// not depend on the drive, so a drive that moved within the set is found by
// its id.
func (s *Set) shardDrive(layout erasure.Layout, i int) drive.Drive {
	want := layout.Drives[i]
	if want.IsZero() {
		return nil
	}
	if s.drives[i].ID() == want {
		return s.drives[i]
	}
	for _, d := range s.drives {
		if d.ID() == want {
			return d
		}
	}
	return nil
}

// shardID returns the id of shard i of object id.
func shardID(id ids.DataID, i int) ids.ShardID {
	// #nosec G115 -- i indexes a shard of the set, so 0 <= i < k+m <= maxShards (256), which New enforces
	return ids.ShardID{Data: id, Index: uint16(i)}
}

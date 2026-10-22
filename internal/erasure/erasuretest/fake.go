package erasuretest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// FakeSet is an in-memory erasure.Set that keeps whole objects and performs
// no coding or placement. Use it in the tests of packages that consume a Set.
// It does not model faults; use the conformance suite for real sets.
type FakeSet struct {
	id     ids.SetID
	k, m   int
	drives []ids.DriveID // one distinct drive id per shard

	mu      sync.Mutex
	objects map[ids.DataID][]byte
}

var _ erasure.Set = (*FakeSet)(nil)

// NewFakeSet returns an empty FakeSet with the given shape. It draws one
// random drive id per shard, so dataShards must be at least 1 and
// parityShards must not be negative.
func NewFakeSet(id ids.SetID, dataShards, parityShards int) *FakeSet {
	drives := make([]ids.DriveID, dataShards+parityShards)
	for i := range drives {
		drives[i] = ids.NewDriveID()
	}
	return &FakeSet{id: id, k: dataShards, m: parityShards, drives: drives, objects: make(map[ids.DataID][]byte)}
}

// ID implements erasure.Set.
func (s *FakeSet) ID() ids.SetID { return s.id }

// Shape implements erasure.Set.
func (s *FakeSet) Shape() (dataShards, parityShards int) { return s.k, s.m }

// Write implements erasure.Set. It stores exactly size bytes.
func (s *FakeSet) Write(ctx context.Context, id ids.DataID, r io.Reader, size int64) (erasure.Layout, error) {
	if err := ctx.Err(); err != nil {
		return erasure.Layout{}, err
	}
	if size < 0 {
		return erasure.Layout{}, fmt.Errorf("erasuretest: negative size %d", size)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return erasure.Layout{}, fmt.Errorf("erasuretest: read %d bytes: %w", size, err)
	}
	s.mu.Lock()
	s.objects[id] = data
	s.mu.Unlock()
	return s.layout(), nil
}

// layout returns a layout with one non-zero drive id per shard.
func (s *FakeSet) layout() erasure.Layout {
	return erasure.Layout{
		Set:          s.id,
		DataShards:   s.k,
		ParityShards: s.m,
		StripeSize:   erasure.DefaultStripeSize,
		Drives:       append([]ids.DriveID(nil), s.drives...),
	}
}

// Read implements erasure.Set. An object that was never written, or was
// deleted, yields erasure.ErrReadQuorum.
func (s *FakeSet) Read(ctx context.Context, id ids.DataID, _ erasure.Layout, size, offset, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offset < 0 || length < 0 || offset > size || length > size-offset {
		return nil, fmt.Errorf("erasuretest: range [%d,%d) outside %d bytes", offset, offset+length, size)
	}
	s.mu.Lock()
	data, ok := s.objects[id]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("erasuretest: object %s: %w", id, erasure.ErrReadQuorum)
	}
	return io.NopCloser(bytes.NewReader(data[offset : offset+length])), nil
}

// Delete implements erasure.Set. Deleting a missing object is not an error.
func (s *FakeSet) Delete(ctx context.Context, id ids.DataID, _ erasure.Layout) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.objects, id)
	s.mu.Unlock()
	return nil
}

// Heal implements erasure.Set. The fake never damages data, so it returns
// the layout unchanged.
func (s *FakeSet) Heal(ctx context.Context, _ ids.DataID, layout erasure.Layout, _ int64) (erasure.Layout, error) {
	return layout, ctx.Err()
}

// FakeRegistry is an erasure.Registry over a fixed list of sets.
type FakeRegistry struct {
	sets []erasure.Set
	next atomic.Uint64
}

var _ erasure.Registry = (*FakeRegistry)(nil)

// NewFakeRegistry returns a registry over sets. Place rotates through them.
func NewFakeRegistry(sets ...erasure.Set) *FakeRegistry {
	return &FakeRegistry{sets: sets}
}

// Place implements erasure.Registry with round-robin placement.
func (r *FakeRegistry) Place(ctx context.Context, _, _ string, _ int64) (erasure.Set, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(r.sets) == 0 {
		return nil, fmt.Errorf("erasuretest: no sets: %w", erasure.ErrUnknownSet)
	}
	n := r.next.Add(1) - 1
	return r.sets[n%uint64(len(r.sets))], nil
}

// Get implements erasure.Registry.
func (r *FakeRegistry) Get(id ids.SetID) (erasure.Set, error) {
	for _, s := range r.sets {
		if s.ID() == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("erasuretest: set %d: %w", id, erasure.ErrUnknownSet)
}

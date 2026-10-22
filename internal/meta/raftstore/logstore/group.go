package logstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/cockroachdb/pebble/v2"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/hashicorp/raft"
)

// GroupStore is the Raft storage of one group. It implements raft.LogStore,
// raft.MonotonicLogStore and raft.StableStore. Obtain it with Store.Group.
//
// Writes to one group are serialized, and each write is committed with
// pebble.Sync before it returns. FirstIndex and LastIndex read cached values
// and never wait for a write in progress.
type GroupStore struct {
	store *Store
	id    ids.PartitionID

	// writeMu serializes the log writes of this group. It is held across the
	// commit, so the cached bounds always describe the committed log.
	writeMu sync.Mutex
	// first and last are the lowest and highest index present, or 0 when the
	// group has no logs. Only a holder of writeMu stores them.
	first atomic.Uint64
	last  atomic.Uint64
}

// Compile-time checks that GroupStore implements the Raft storage interfaces.
var (
	_ raft.LogStore          = (*GroupStore)(nil)
	_ raft.MonotonicLogStore = (*GroupStore)(nil)
	_ raft.StableStore       = (*GroupStore)(nil)
)

// FirstIndex implements raft.LogStore. It returns 0 when the group has no logs.
func (g *GroupStore) FirstIndex() (uint64, error) {
	done, err := g.store.enter()
	if err != nil {
		return 0, err
	}
	defer done()
	return g.first.Load(), nil
}

// LastIndex implements raft.LogStore. It returns 0 when the group has no logs.
func (g *GroupStore) LastIndex() (uint64, error) {
	done, err := g.store.enter()
	if err != nil {
		return 0, err
	}
	defer done()
	return g.last.Load(), nil
}

// IsMonotonic implements raft.MonotonicLogStore. It returns true because
// StoreLogs never leaves a gap between indices; see the package documentation.
func (g *GroupStore) IsMonotonic() bool {
	return true
}

// GetLog implements raft.LogStore. It returns raft.ErrLogNotFound when index
// is not stored, and an error when log is nil.
func (g *GroupStore) GetLog(index uint64, log *raft.Log) error {
	done, err := g.store.enter()
	if err != nil {
		return err
	}
	defer done()
	if log == nil {
		return fmt.Errorf("logstore: get log %d of group %d: nil output", index, g.id)
	}

	value, closer, err := g.store.db.Get(logKey(g.id, index))
	if errors.Is(err, pebble.ErrNotFound) {
		return raft.ErrLogNotFound
	}
	if err != nil {
		return fmt.Errorf("logstore: get log %d of group %d: %w", index, g.id, err)
	}
	decoded, decodeErr := decodeLog(value)
	closeErr := closer.Close()
	if decodeErr != nil {
		return fmt.Errorf("logstore: get log %d of group %d: %w", index, g.id, decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("logstore: get log %d of group %d: %w", index, g.id, closeErr)
	}
	if decoded.Index != index {
		return fmt.Errorf("logstore: get log %d of group %d: stored entry has index %d", index, g.id, decoded.Index)
	}
	*log = decoded
	return nil
}

// StoreLog implements raft.LogStore. It is StoreLogs with one entry.
func (g *GroupStore) StoreLog(log *raft.Log) error {
	return g.StoreLogs([]*raft.Log{log})
}

// StoreLogs implements raft.LogStore. All entries of one call are committed
// in one batch with pebble.Sync, so the call stores all of them or none.
//
// The entries must continue the log. Within the call, indices must be
// consecutive and non-zero. If the group has logs, the first index must be
// the group's last index plus one. An empty group accepts any starting index.
// Violations return an error and store nothing. An empty call is a no-op.
func (g *GroupStore) StoreLogs(logs []*raft.Log) error {
	done, err := g.store.enter()
	if err != nil {
		return err
	}
	defer done()
	if len(logs) == 0 {
		return nil
	}
	if err := checkRun(logs); err != nil {
		return fmt.Errorf("logstore: store logs of group %d: %w", g.id, err)
	}

	batch := g.store.db.NewBatch()
	defer func() { _ = batch.Close() }()
	for _, l := range logs {
		value, err := encodeLog(l)
		if err != nil {
			return fmt.Errorf("logstore: store logs of group %d: %w", g.id, err)
		}
		if err := batch.Set(logKey(g.id, l.Index), value, nil); err != nil {
			return fmt.Errorf("logstore: store logs of group %d: %w", g.id, err)
		}
	}

	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	first, last := g.first.Load(), g.last.Load()
	if first != 0 && logs[0].Index != last+1 {
		return fmt.Errorf("logstore: store logs of group %d: index %d does not follow last index %d",
			g.id, logs[0].Index, last)
	}
	if err := g.store.db.Apply(batch, pebble.Sync); err != nil {
		return fmt.Errorf("logstore: store logs of group %d: %w", g.id, err)
	}
	if first == 0 {
		g.first.Store(logs[0].Index)
	}
	g.last.Store(logs[len(logs)-1].Index)
	return nil
}

// DeleteRange implements raft.LogStore. It removes the entries with indices
// in [min, max] in one committed batch. An empty range (min > max) and a range
// with no entries are no-ops. Entries of other groups are never touched.
// The cached first and last index are updated to the remaining entries.
func (g *GroupStore) DeleteRange(min, max uint64) error {
	done, err := g.store.enter()
	if err != nil {
		return err
	}
	defer done()
	if min > max {
		return nil
	}

	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	first, last := g.first.Load(), g.last.Load()
	if first == 0 || max < first || min > last {
		return nil
	}

	// The new bounds are read before the commit. Entries that survive lie
	// outside [min, max], so these reads give the bounds after the delete.
	newFirst, newLast := first, last
	if min <= first && first <= max {
		newFirst, err = g.store.lowestAbove(g.id, max)
		if err != nil {
			return fmt.Errorf("logstore: delete range of group %d: %w", g.id, err)
		}
	}
	if min <= last && last <= max {
		newLast, err = g.store.highestBelow(g.id, min)
		if err != nil {
			return fmt.Errorf("logstore: delete range of group %d: %w", g.id, err)
		}
	}

	batch := g.store.db.NewBatch()
	defer func() { _ = batch.Close() }()
	if err := batch.DeleteRange(logKey(g.id, min), logEndExclusive(g.id, max), nil); err != nil {
		return fmt.Errorf("logstore: delete range of group %d: %w", g.id, err)
	}
	if err := g.store.db.Apply(batch, pebble.Sync); err != nil {
		return fmt.Errorf("logstore: delete range of group %d: %w", g.id, err)
	}
	g.first.Store(newFirst)
	g.last.Store(newLast)
	return nil
}

// Set implements raft.StableStore. The key and value are copied.
func (g *GroupStore) Set(key, val []byte) error {
	done, err := g.store.enter()
	if err != nil {
		return err
	}
	defer done()
	return g.set(key, val)
}

// Get implements raft.StableStore. A missing key returns an empty slice and no
// error. The returned slice is a copy.
func (g *GroupStore) Get(key []byte) ([]byte, error) {
	done, err := g.store.enter()
	if err != nil {
		return nil, err
	}
	defer done()
	value, found, err := g.get(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return []byte{}, nil
	}
	return value, nil
}

// SetUint64 implements raft.StableStore. The value is stored as 8 big-endian
// bytes.
func (g *GroupStore) SetUint64(key []byte, val uint64) error {
	done, err := g.store.enter()
	if err != nil {
		return err
	}
	defer done()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], val)
	return g.set(key, buf[:])
}

// GetUint64 implements raft.StableStore. A missing key returns 0 and no error.
// A value that is not 8 bytes long is an error.
func (g *GroupStore) GetUint64(key []byte) (uint64, error) {
	done, err := g.store.enter()
	if err != nil {
		return 0, err
	}
	defer done()
	value, found, err := g.get(key)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	if len(value) != 8 {
		return 0, fmt.Errorf("logstore: get uint64 of group %d: value has %d bytes, want 8", g.id, len(value))
	}
	return binary.BigEndian.Uint64(value), nil
}

// set writes one stable value with pebble.Sync. The caller must hold the store
// open; set does not enter the store itself.
func (g *GroupStore) set(key, val []byte) error {
	if err := g.store.db.Set(stableKey(g.id, key), val, pebble.Sync); err != nil {
		return fmt.Errorf("logstore: set stable key of group %d: %w", g.id, err)
	}
	return nil
}

// get returns a copy of the stable value of key and whether it exists. The
// caller must hold the store open.
func (g *GroupStore) get(key []byte) ([]byte, bool, error) {
	value, closer, err := g.store.db.Get(stableKey(g.id, key))
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("logstore: get stable key of group %d: %w", g.id, err)
	}
	out := make([]byte, len(value))
	copy(out, value)
	if err := closer.Close(); err != nil {
		return nil, false, fmt.Errorf("logstore: get stable key of group %d: %w", g.id, err)
	}
	return out, true, nil
}

// checkRun reports why logs cannot be appended as one run: a nil entry, an
// index of 0, or an index that is not one more than the previous entry's.
func checkRun(logs []*raft.Log) error {
	for i, l := range logs {
		if l == nil {
			return fmt.Errorf("entry %d is nil", i)
		}
		if l.Index == 0 {
			return fmt.Errorf("entry %d has index 0, which is reserved", i)
		}
		if i > 0 && l.Index != logs[i-1].Index+1 {
			return fmt.Errorf("entry %d has index %d, want %d", i, l.Index, logs[i-1].Index+1)
		}
	}
	return nil
}

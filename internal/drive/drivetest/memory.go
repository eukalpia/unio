// Package drivetest provides an in-memory drive for tests of other packages
// and RunSuite, the conformance suite every drive.Drive implementation must
// pass.
package drivetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

// Faults injects failures into a Memory drive. Each hook is optional. A
// non-nil error returned by a hook fails the matching call without changing
// any state.
type Faults struct {
	// Write is consulted before WriteShard stores a shard.
	Write func(id ids.ShardID) error
	// Read is consulted before ReadShard and StatShard.
	Read func(id ids.ShardID) error
	// Delete is consulted before DeleteShard.
	Delete func(id ids.ShardID) error
}

// memoryCapacity is the capacity Memory reports from Info.
const memoryCapacity = 1 << 40

// Memory is an in-memory drive.Drive. It is safe for concurrent use and
// honours the drive.Drive contract, so it is a reference for tests in other
// packages.
type Memory struct {
	id     ids.DriveID
	faults Faults

	mu     sync.RWMutex
	shards map[ids.ShardID]memShard
	closed bool
}

type memShard struct {
	data    []byte
	created time.Time
}

// MemoryOption configures a Memory drive.
type MemoryOption func(*Memory)

// WithFaults installs fault hooks.
func WithFaults(f Faults) MemoryOption {
	return func(m *Memory) { m.faults = f }
}

// NewMemory returns an empty in-memory drive with the given id.
func NewMemory(id ids.DriveID, opts ...MemoryOption) *Memory {
	m := &Memory{id: id, shards: make(map[ids.ShardID]memShard)}
	for _, o := range opts {
		o(m)
	}
	return m
}

// ID implements drive.Drive.
func (m *Memory) ID() ids.DriveID { return m.id }

// WriteShard implements drive.Drive.
func (m *Memory) WriteShard(ctx context.Context, id ids.ShardID, r io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("drivetest: negative size %d", size)
	}
	if err := m.checkOpen(); err != nil {
		return err
	}
	if m.has(id) {
		return drive.ErrShardExists
	}
	if m.faults.Write != nil {
		if err := m.faults.Write(id); err != nil {
			return err
		}
	}
	data, err := readExactly(r, size)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return drive.ErrClosed
	}
	if _, ok := m.shards[id]; ok {
		return drive.ErrShardExists
	}
	m.shards[id] = memShard{data: data, created: time.Now()}
	return nil
}

// ReadShard implements drive.Drive.
func (m *Memory) ReadShard(ctx context.Context, id ids.ShardID, offset, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.checkOpen(); err != nil {
		return nil, err
	}
	if m.faults.Read != nil {
		if err := m.faults.Read(id); err != nil {
			return nil, err
		}
	}
	sh, err := m.get(id)
	if err != nil {
		return nil, err
	}
	end, err := ResolveRange(int64(len(sh.data)), offset, length)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(sh.data[offset:end])), nil
}

// StatShard implements drive.Drive.
func (m *Memory) StatShard(ctx context.Context, id ids.ShardID) (drive.ShardInfo, error) {
	if err := ctx.Err(); err != nil {
		return drive.ShardInfo{}, err
	}
	if err := m.checkOpen(); err != nil {
		return drive.ShardInfo{}, err
	}
	if m.faults.Read != nil {
		if err := m.faults.Read(id); err != nil {
			return drive.ShardInfo{}, err
		}
	}
	sh, err := m.get(id)
	if err != nil {
		return drive.ShardInfo{}, err
	}
	return drive.ShardInfo{ID: id, Size: int64(len(sh.data)), Created: sh.created}, nil
}

// DeleteShard implements drive.Drive.
func (m *Memory) DeleteShard(ctx context.Context, id ids.ShardID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.checkOpen(); err != nil {
		return err
	}
	if m.faults.Delete != nil {
		if err := m.faults.Delete(id); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return drive.ErrClosed
	}
	if _, ok := m.shards[id]; !ok {
		return drive.ErrShardNotFound
	}
	delete(m.shards, id)
	return nil
}

// WalkShards implements drive.Drive. It iterates over a snapshot, so fn may
// call other methods of the drive.
func (m *Memory) WalkShards(ctx context.Context, fn func(drive.ShardInfo) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.checkOpen(); err != nil {
		return err
	}
	m.mu.RLock()
	infos := make([]drive.ShardInfo, 0, len(m.shards))
	for id, sh := range m.shards {
		infos = append(infos, drive.ShardInfo{ID: id, Size: int64(len(sh.data)), Created: sh.created})
	}
	m.mu.RUnlock()
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID.String() < infos[j].ID.String() })
	for _, info := range infos {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(info); err != nil {
			return err
		}
	}
	return nil
}

// Info implements drive.Drive.
func (m *Memory) Info(ctx context.Context) (drive.Info, error) {
	if err := ctx.Err(); err != nil {
		return drive.Info{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return drive.Info{}, drive.ErrClosed
	}
	var used uint64
	for _, sh := range m.shards {
		used += uint64(len(sh.data))
	}
	return drive.Info{
		ID:     m.id,
		Total:  memoryCapacity,
		Used:   used,
		Free:   memoryCapacity - used,
		Online: !m.closed,
	}, nil
}

// Close implements drive.Drive. Calling Close twice returns drive.ErrClosed.
func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return drive.ErrClosed
	}
	m.closed = true
	m.shards = nil
	return nil
}

func (m *Memory) checkOpen() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return drive.ErrClosed
	}
	return nil
}

func (m *Memory) has(id ids.ShardID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.shards[id]
	return ok
}

func (m *Memory) get(id ids.ShardID) (memShard, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return memShard{}, drive.ErrClosed
	}
	sh, ok := m.shards[id]
	if !ok {
		return memShard{}, drive.ErrShardNotFound
	}
	return sh, nil
}

// readExactly reads exactly size bytes from r. It fails if r has fewer or
// more bytes, or if it returns an error.
func readExactly(r io.Reader, size int64) ([]byte, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("drivetest: reading shard: %w", err)
	}
	var one [1]byte
	n, err := r.Read(one[:])
	if n > 0 {
		return nil, fmt.Errorf("drivetest: reader holds more than %d bytes", size)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("drivetest: reading shard: %w", err)
	}
	return data, nil
}

// ResolveRange validates a read range against a shard of the given size and
// returns the exclusive end offset. A negative length reads to the end. The
// offset may equal size only for an empty read.
func ResolveRange(size, offset, length int64) (int64, error) {
	if offset < 0 || offset > size {
		return 0, fmt.Errorf("drivetest: offset %d outside shard of %d bytes", offset, size)
	}
	if length < 0 {
		return size, nil
	}
	if length > size-offset {
		return 0, fmt.Errorf("drivetest: range [%d, %d) outside shard of %d bytes", offset, offset+length, size)
	}
	return offset + length, nil
}

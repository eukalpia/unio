package clustertest

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

// MemDrive is an in-memory drive.Drive. It follows the drive contract:
// shards are immutable, a write stores exactly the declared number of bytes or
// nothing, and reads are bounds checked. Tests use it wherever a drive is
// needed without a disk. MemDrive is safe for concurrent use.
type MemDrive struct {
	id   ids.DriveID
	node ids.NodeID

	mu     sync.RWMutex
	shards map[ids.ShardID]memShard
	closed bool
	// fault, when set, is returned by every operation. It simulates a drive
	// that has gone offline or faulty.
	fault error
	// capacity is the reported total size in bytes.
	capacity uint64
}

type memShard struct {
	data    []byte
	created time.Time
}

// NewMemDrive returns an empty drive with the given identity and capacity in
// bytes. A zero capacity reports 1 TiB.
func NewMemDrive(id ids.DriveID, node ids.NodeID, capacity uint64) *MemDrive {
	if capacity == 0 {
		capacity = 1 << 40
	}
	return &MemDrive{
		id:       id,
		node:     node,
		shards:   map[ids.ShardID]memShard{},
		capacity: capacity,
	}
}

// SetFault makes every subsequent operation fail with err. Passing nil
// restores normal behaviour.
func (d *MemDrive) SetFault(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fault = err
}

// check returns the error an operation must fail with, or nil. The caller
// holds d.mu.
func (d *MemDrive) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.closed {
		return drive.ErrClosed
	}
	return d.fault
}

// ID implements drive.Drive.
func (d *MemDrive) ID() ids.DriveID { return d.id }

// WriteShard implements drive.Drive. It reads exactly size bytes and stores
// them only if the stream yields exactly size bytes.
func (d *MemDrive) WriteShard(ctx context.Context, id ids.ShardID, r io.Reader, size int64) error {
	if size < 0 {
		return fmt.Errorf("clustertest: negative shard size %d", size)
	}
	d.mu.RLock()
	err := d.check(ctx)
	_, exists := d.shards[id]
	d.mu.RUnlock()
	if err != nil {
		return err
	}
	if exists {
		return drive.ErrShardExists
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("clustertest: write %s: read %d bytes: %w", id, size, err)
	}
	var extra [1]byte
	n, err := r.Read(extra[:])
	if n > 0 {
		return fmt.Errorf("clustertest: write %s: stream longer than %d bytes", id, size)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("clustertest: write %s: %w", id, err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.check(ctx); err != nil {
		return err
	}
	if _, ok := d.shards[id]; ok {
		return drive.ErrShardExists
	}
	d.shards[id] = memShard{data: data, created: time.Now()}
	return nil
}

// ReadShard implements drive.Drive. A negative length reads to the end.
func (d *MemDrive) ReadShard(ctx context.Context, id ids.ShardID, offset, length int64) (io.ReadCloser, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if err := d.check(ctx); err != nil {
		return nil, err
	}
	s, ok := d.shards[id]
	if !ok {
		return nil, drive.ErrShardNotFound
	}
	size := int64(len(s.data))
	if offset < 0 || offset > size {
		return nil, fmt.Errorf("clustertest: read %s: offset %d outside shard of %d bytes", id, offset, size)
	}
	end := size
	if length >= 0 {
		if length > size-offset {
			return nil, fmt.Errorf("clustertest: read %s: range [%d, %d) beyond %d bytes", id, offset, offset+length, size)
		}
		end = offset + length
	}
	// The slice aliases the stored bytes, which are never modified after
	// the write, so the reader needs no copy.
	return io.NopCloser(bytes.NewReader(s.data[offset:end])), nil
}

// StatShard implements drive.Drive.
func (d *MemDrive) StatShard(ctx context.Context, id ids.ShardID) (drive.ShardInfo, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if err := d.check(ctx); err != nil {
		return drive.ShardInfo{}, err
	}
	s, ok := d.shards[id]
	if !ok {
		return drive.ShardInfo{}, drive.ErrShardNotFound
	}
	return drive.ShardInfo{ID: id, Size: int64(len(s.data)), Created: s.created}, nil
}

// DeleteShard implements drive.Drive.
func (d *MemDrive) DeleteShard(ctx context.Context, id ids.ShardID) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.check(ctx); err != nil {
		return err
	}
	if _, ok := d.shards[id]; !ok {
		return drive.ErrShardNotFound
	}
	delete(d.shards, id)
	return nil
}

// WalkShards implements drive.Drive. Shards are visited in ascending id order.
// The walk works on a snapshot, so fn may call back into the drive.
func (d *MemDrive) WalkShards(ctx context.Context, fn func(drive.ShardInfo) error) error {
	d.mu.RLock()
	if err := d.check(ctx); err != nil {
		d.mu.RUnlock()
		return err
	}
	infos := make([]drive.ShardInfo, 0, len(d.shards))
	for id, s := range d.shards {
		infos = append(infos, drive.ShardInfo{ID: id, Size: int64(len(s.data)), Created: s.created})
	}
	d.mu.RUnlock()

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].ID.String() < infos[j].ID.String()
	})
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
func (d *MemDrive) Info(ctx context.Context) (drive.Info, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if err := d.check(ctx); err != nil {
		return drive.Info{}, err
	}
	var used uint64
	for _, s := range d.shards {
		used += uint64(len(s.data))
	}
	free := uint64(0)
	if used < d.capacity {
		free = d.capacity - used
	}
	return drive.Info{
		ID:       d.id,
		Node:     d.node,
		Endpoint: "mem://" + d.id.String(),
		Total:    d.capacity,
		Free:     free,
		Used:     used,
		Online:   true,
	}, nil
}

// Close implements drive.Drive. Later calls fail with drive.ErrClosed. Close
// is idempotent.
func (d *MemDrive) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

package erasuretest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

// Faults configures the behaviour of a Drive. The zero value is a healthy
// drive.
type Faults struct {
	// Offline makes every call fail with drive.ErrOffline.
	Offline bool
	// Hang makes every call block until its context is done, then return
	// the context error.
	Hang bool
	// Latency is added to every call. Context cancellation interrupts it.
	Latency time.Duration
	// WriteErr makes WriteShard fail with this error.
	WriteErr error
	// WriteErrAfter, when positive together with WriteErr, lets WriteShard
	// consume about that many bytes before failing, simulating a drive that
	// dies mid-stream.
	WriteErrAfter int64
	// ReadErr makes ReadShard fail with this error.
	ReadErr error
}

// storedShard is one shard held by a Drive.
type storedShard struct {
	data    []byte
	created time.Time
}

// Drive is an in-memory drive.Drive with fault injection. It is safe for
// concurrent use. Stored shards are never modified in place, so readers that
// are open when a fault is injected keep the bytes they started with.
type Drive struct {
	id     ids.DriveID
	mu     sync.Mutex
	shards map[ids.ShardID]storedShard
	faults Faults
	closed bool
}

var _ drive.Drive = (*Drive)(nil)

// NewDrive returns an empty, healthy drive with a random id.
func NewDrive() *Drive {
	return &Drive{id: ids.NewDriveID(), shards: make(map[ids.ShardID]storedShard)}
}

// SetFaults replaces the fault configuration of d.
func (d *Drive) SetFaults(f Faults) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.faults = f
}

// Shard returns a copy of the bytes stored for id, and whether it exists.
func (d *Drive) Shard(id ids.ShardID) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.shards[id]
	if !ok {
		return nil, false
	}
	return bytes.Clone(s.data), true
}

// ShardCount returns the number of shards stored on d, ignoring faults.
func (d *Drive) ShardCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.shards)
}

// Corrupt inverts every bit of the byte at offset in the stored shard id.
// It returns an error if the shard or the offset does not exist.
func (d *Drive) Corrupt(id ids.ShardID, offset int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.shards[id]
	if !ok {
		return fmt.Errorf("erasuretest: corrupt %s: %w", id, drive.ErrShardNotFound)
	}
	if offset < 0 || offset >= len(s.data) {
		return fmt.Errorf("erasuretest: corrupt %s at %d: outside %d bytes", id, offset, len(s.data))
	}
	data := bytes.Clone(s.data)
	data[offset] ^= 0xFF
	s.data = data
	d.shards[id] = s
	return nil
}

// ID returns the drive id.
func (d *Drive) ID() ids.DriveID {
	return d.id
}

// gate applies the closed, offline, hang and latency faults and returns the
// fault configuration for the caller to apply the remaining ones.
func (d *Drive) gate(ctx context.Context) (Faults, error) {
	d.mu.Lock()
	closed, f := d.closed, d.faults
	d.mu.Unlock()
	switch {
	case closed:
		return f, drive.ErrClosed
	case f.Offline:
		return f, drive.ErrOffline
	case f.Hang:
		<-ctx.Done()
		return f, ctx.Err()
	}
	if f.Latency > 0 {
		t := time.NewTimer(f.Latency)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return f, ctx.Err()
		}
	}
	return f, nil
}

// WriteShard implements drive.Drive. It reads exactly size bytes and stores
// them only if the whole shard arrived intact.
func (d *Drive) WriteShard(ctx context.Context, id ids.ShardID, r io.Reader, size int64) error {
	f, err := d.gate(ctx)
	if err != nil {
		return err
	}
	if f.WriteErr != nil && f.WriteErrAfter <= 0 {
		return f.WriteErr
	}
	data, err := readShard(r, size, f)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.shards[id]; ok {
		return drive.ErrShardExists
	}
	d.shards[id] = storedShard{data: data, created: time.Now()}
	return nil
}

// readShard reads exactly size bytes from r, applying the mid-stream write
// fault, and verifies that r ends there.
func readShard(r io.Reader, size int64, f Faults) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("erasuretest: negative shard size %d", size)
	}
	data := make([]byte, size)
	const chunk = 32 << 10
	var n int64
	for n < size {
		c := min(int64(chunk), size-n)
		if _, err := io.ReadFull(r, data[n:n+c]); err != nil {
			return nil, fmt.Errorf("erasuretest: shard ended after %d of %d bytes: %w", n, size, err)
		}
		n += c
		if f.WriteErr != nil && f.WriteErrAfter > 0 && n >= f.WriteErrAfter {
			return nil, f.WriteErr
		}
	}
	var extra [1]byte
	switch _, err := io.ReadFull(r, extra[:]); {
	case err == nil:
		return nil, fmt.Errorf("erasuretest: shard longer than %d bytes", size)
	case errors.Is(err, io.EOF):
		return data, nil
	default:
		return nil, fmt.Errorf("erasuretest: read after shard: %w", err)
	}
}

// ReadShard implements drive.Drive. A negative length reads to the end.
func (d *Drive) ReadShard(ctx context.Context, id ids.ShardID, offset, length int64) (io.ReadCloser, error) {
	f, err := d.gate(ctx)
	if err != nil {
		return nil, err
	}
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	d.mu.Lock()
	s, ok := d.shards[id]
	d.mu.Unlock()
	if !ok {
		return nil, drive.ErrShardNotFound
	}
	end := int64(len(s.data))
	if length >= 0 {
		end = offset + length
	}
	if offset < 0 || offset > int64(len(s.data)) || end > int64(len(s.data)) {
		return nil, fmt.Errorf("erasuretest: read [%d,%d) of %d-byte shard %s: %w",
			offset, end, len(s.data), id, drive.ErrCorrupt)
	}
	return io.NopCloser(bytes.NewReader(s.data[offset:end])), nil
}

// StatShard implements drive.Drive.
func (d *Drive) StatShard(ctx context.Context, id ids.ShardID) (drive.ShardInfo, error) {
	if _, err := d.gate(ctx); err != nil {
		return drive.ShardInfo{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.shards[id]
	if !ok {
		return drive.ShardInfo{}, drive.ErrShardNotFound
	}
	return drive.ShardInfo{ID: id, Size: int64(len(s.data)), Created: s.created}, nil
}

// DeleteShard implements drive.Drive.
func (d *Drive) DeleteShard(ctx context.Context, id ids.ShardID) error {
	if _, err := d.gate(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.shards[id]; !ok {
		return drive.ErrShardNotFound
	}
	delete(d.shards, id)
	return nil
}

// WalkShards implements drive.Drive. It visits a snapshot of the shards.
func (d *Drive) WalkShards(ctx context.Context, fn func(drive.ShardInfo) error) error {
	if _, err := d.gate(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	infos := make([]drive.ShardInfo, 0, len(d.shards))
	for id, s := range d.shards {
		infos = append(infos, drive.ShardInfo{ID: id, Size: int64(len(s.data)), Created: s.created})
	}
	d.mu.Unlock()
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
func (d *Drive) Info(context.Context) (drive.Info, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return drive.Info{
		ID:     d.id,
		Total:  1 << 40,
		Free:   1 << 40,
		Online: !d.closed && !d.faults.Offline,
	}, nil
}

// Close implements drive.Drive. Later calls fail with drive.ErrClosed.
func (d *Drive) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

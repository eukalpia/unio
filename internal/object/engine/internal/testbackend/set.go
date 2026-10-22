package testbackend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sync"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// maxDrives bounds the drives of a set. Shard indices are uint16, so a set
// never has more drives than this.
const maxDrives = 1 << 15

// Set is an in-memory erasure.Set. Every drive holds a full copy of each
// shard, each copy with its own digest. Writes need the write quorum of
// drives, reads verify the digest of a copy before serving it, and a copy
// that fails verification is skipped in favour of another one.
type Set struct {
	id     ids.SetID
	k, m   int
	mu     sync.Mutex
	drives []*drive
	byID   map[ids.DriveID]*drive
}

type drive struct {
	id ids.DriveID
	// index is the position of the drive in its set, and the shard index of
	// every copy it holds.
	index      uint16
	failWrites bool
	shards     map[ids.ShardID]shard
}

type shard struct {
	data []byte
	sum  [sha256.Size]byte
}

var _ erasure.Set = (*Set)(nil)

// NewSet returns a set with k data and m parity shards, one drive per shard.
// It panics on a shape no set can have; that is a programming error in the
// test that builds the set.
func NewSet(id ids.SetID, k, m int) *Set {
	if k < 1 || m < 0 || k+m > maxDrives {
		panic(fmt.Sprintf("testbackend: invalid erasure shape %d+%d", k, m))
	}
	s := &Set{id: id, k: k, m: m, byID: make(map[ids.DriveID]*drive, k+m)}
	for i := 0; i < k+m; i++ {
		// i < k+m <= maxDrives, checked above, so the index fits in a uint16.
		d := &drive{
			id:     ids.NewDriveID(),
			index:  uint16(i), // #nosec G115 -- i is below maxDrives, checked above
			shards: make(map[ids.ShardID]shard),
		}
		s.drives = append(s.drives, d)
		s.byID[d.id] = d
	}
	return s
}

// shardKey names the copy of id held by drive d.
func shardKey(id ids.DataID, d *drive) ids.ShardID {
	return ids.ShardID{Data: id, Index: d.index}
}

// ID implements erasure.Set.
func (s *Set) ID() ids.SetID { return s.id }

// Shape implements erasure.Set.
func (s *Set) Shape() (dataShards, parityShards int) { return s.k, s.m }

// Write implements erasure.Set. The payload is read completely before any
// drive is touched, so a failing reader leaves nothing behind.
func (s *Set) Write(ctx context.Context, id ids.DataID, r io.Reader, size int64) (erasure.Layout, error) {
	if size < 0 {
		return erasure.Layout{}, fmt.Errorf("testbackend: negative size %d", size)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return erasure.Layout{}, fmt.Errorf("testbackend: read payload: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return erasure.Layout{}, err
	}
	sum := sha256.Sum256(buf)
	layout := erasure.Layout{
		Set:          s.id,
		DataShards:   s.k,
		ParityShards: s.m,
		StripeSize:   erasure.DefaultStripeSize,
		Drives:       make([]ids.DriveID, s.k+s.m),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var written []*drive
	for i, d := range s.drives {
		if d.failWrites {
			continue
		}
		d.shards[shardKey(id, d)] = shard{data: buf, sum: sum}
		layout.Drives[i] = d.id
		written = append(written, d)
	}
	if layout.Written() < layout.WriteQuorum() {
		for _, d := range written {
			delete(d.shards, shardKey(id, d))
		}
		return erasure.Layout{}, fmt.Errorf("testbackend: %d of %d drives written: %w",
			layout.Written(), layout.WriteQuorum(), erasure.ErrWriteQuorum)
	}
	return layout, nil
}

// Read implements erasure.Set. It serves the window from the first copy
// whose digest verifies.
func (s *Set) Read(ctx context.Context, id ids.DataID, layout erasure.Layout, size, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length < 0 || offset+length > size {
		return nil, fmt.Errorf("testbackend: window [%d,%d) outside %d bytes", offset, offset+length, size)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	corrupt := false
	for _, did := range layout.Drives {
		if did.IsZero() {
			continue
		}
		d, ok := s.byID[did]
		if !ok {
			continue
		}
		sh, ok := d.shards[shardKey(id, d)]
		if !ok {
			continue
		}
		if sha256.Sum256(sh.data) != sh.sum || int64(len(sh.data)) != size {
			corrupt = true
			continue
		}
		return io.NopCloser(bytes.NewReader(sh.data[offset : offset+length])), nil
	}
	if corrupt {
		return nil, fmt.Errorf("testbackend: %s: %w", id, erasure.ErrCorrupt)
	}
	return nil, fmt.Errorf("testbackend: %s: %w", id, erasure.ErrReadQuorum)
}

// Delete implements erasure.Set. Missing shards are not an error.
func (s *Set) Delete(_ context.Context, id ids.DataID, layout erasure.Layout) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, did := range layout.Drives {
		if d, ok := s.byID[did]; ok {
			delete(d.shards, shardKey(id, d))
		}
	}
	return nil
}

// Heal implements erasure.Set. The fake never loses shards on its own, so
// there is nothing to repair and the layout is returned unchanged.
func (s *Set) Heal(_ context.Context, _ ids.DataID, layout erasure.Layout, _ int64) (erasure.Layout, error) {
	return layout, nil
}

// FailWrites makes every drive of the set reject writes while on is true.
func (s *Set) FailWrites(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.drives {
		d.failWrites = on
	}
}

// FailDrive makes drive i reject writes while on is true.
func (s *Set) FailDrive(i int, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drives[i].failWrites = on
}

// Corrupt flips a byte in the copy of id held by drive i. It reports whether
// such a copy exists.
func (s *Set) Corrupt(id ids.DataID, i int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.drives[i]
	key := shardKey(id, d)
	sh, ok := d.shards[key]
	if !ok || len(sh.data) == 0 {
		return false
	}
	bad := append([]byte(nil), sh.data...)
	bad[0] ^= 0xff
	sh.data = bad
	d.shards[key] = sh
	return true
}

// dataIDs adds the distinct data ids that have at least one shard copy to into.
func (s *Set) dataIDs(into map[ids.DataID]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.drives {
		for k := range d.shards {
			into[k.Data] = struct{}{}
		}
	}
}

// Package index is the per-drive shard index. It maps ShardID to the physical
// location of the shard (a record in a packed segment or a blob file) and
// records per-segment metadata. It is backed by Pebble.
//
// The index is derived state: when it is missing or unreadable, the drive
// rebuilds it from the segment files and the blob directory. It is not the
// source of truth for data, but it is the source of truth for which records
// are visible.
package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"

	"github.com/eukalpia/unio/internal/drive/local/internal/syncerr"
	"github.com/eukalpia/unio/internal/ids"
)

// Errors returned by the index.
var (
	// ErrNotFound is returned by Get for an id the index does not hold.
	ErrNotFound = errors.New("index: not found")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("index: closed")
	// ErrCorrupt is returned for an entry that cannot describe a location.
	ErrCorrupt = errors.New("index: corrupt entry")
)

// Kind says where a shard's bytes live.
type Kind uint8

// Shard kinds.
const (
	// KindPacked is a record inside a segment file.
	KindPacked Kind = 1
	// KindBlob is a standalone blob file.
	KindBlob Kind = 2
)

// Location is the physical placement of one shard.
type Location struct {
	Kind Kind
	// Segment is the segment id for KindPacked and zero for KindBlob.
	Segment uint64
	// Offset is the offset of the record header for KindPacked and zero for
	// KindBlob.
	Offset int64
	// Length is the shard data length.
	Length int64
	// Created is the write time, in Unix nanoseconds.
	Created int64
}

// SegmentMeta is the committed state of one segment file.
type SegmentMeta struct {
	// End is the offset just past the last committed record.
	End int64
	// Sealed reports that the segment is full or retired from appends.
	Sealed bool
}

// Index is a shard index backed by Pebble. It is safe for concurrent use.
type Index struct {
	mu     sync.RWMutex
	dir    string
	db     *pebble.DB
	closed bool
}

// Open opens or creates the index in dir. cache may be nil, in which case
// Pebble uses its default block cache; a shared cache can be passed to bound
// memory across several drives.
//
// The filesystem is set explicitly, which turns off Pebble's disk-health
// ticker. That ticker only reports slow disks, and it keeps a goroutine alive
// when Open fails, so an unusable index would leak it. Durability does not
// depend on it.
func Open(dir string, cache *pebble.Cache) (*Index, error) {
	opts := &pebble.Options{Cache: cache, FS: vfs.Default}
	db, err := pebble.Open(dir, opts)
	if err != nil {
		return nil, fmt.Errorf("index: open %s: %w", dir, err)
	}
	return &Index{dir: dir, db: db}, nil
}

// read runs fn while the index is open. It returns ErrClosed after Close.
func (x *Index) read(fn func(db *pebble.DB) error) error {
	x.mu.RLock()
	defer x.mu.RUnlock()
	if x.closed {
		return ErrClosed
	}
	return fn(x.db)
}

// Get returns the location of id, or ErrNotFound.
func (x *Index) Get(id ids.ShardID) (Location, error) {
	var loc Location
	err := x.read(func(db *pebble.DB) error {
		v, closer, err := db.Get(shardKey(id))
		if errors.Is(err, pebble.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("index: get %s: %w", id, err)
		}
		defer closer.Close()
		loc, err = decodeLocation(v)
		if err != nil {
			return fmt.Errorf("index: shard %s: %w", id, err)
		}
		return nil
	})
	return loc, err
}

// Entry is one shard and its location.
type Entry struct {
	ID  ids.ShardID
	Loc Location
}

// Page returns up to limit shards in key order, starting at the first shard
// whose id is at least start. Pages are read under the index lock and
// returned, so callers can process them without holding it.
func (x *Index) Page(start ids.ShardID, limit int) ([]Entry, error) {
	var out []Entry
	err := x.read(func(db *pebble.DB) error {
		iter, err := db.NewIter(&pebble.IterOptions{
			LowerBound: shardKey(start),
			UpperBound: []byte{prefixSegment},
		})
		if err != nil {
			return fmt.Errorf("index: iterate: %w", err)
		}
		defer iter.Close()
		for ok := iter.First(); ok && len(out) < limit; ok = iter.Next() {
			id, err := parseShardKey(iter.Key())
			if err != nil {
				return err
			}
			loc, err := decodeLocation(iter.Value())
			if err != nil {
				return fmt.Errorf("index: shard %s: %w", id, err)
			}
			out = append(out, Entry{ID: id, Loc: loc})
		}
		if err := iter.Error(); err != nil {
			return fmt.Errorf("index: iterate: %w", err)
		}
		return nil
	})
	return out, err
}

// walkPage is the number of entries Walk reads per lock acquisition.
const walkPage = 512

// Walk calls fn for every shard in key order. It holds no lock while fn runs,
// so fn may use the index. It stops at the first error returned by fn.
func (x *Index) Walk(fn func(ids.ShardID, Location) error) error {
	start := ids.ShardID{}
	for {
		page, err := x.Page(start, walkPage)
		if err != nil {
			return err
		}
		for _, e := range page {
			if err := fn(e.ID, e.Loc); err != nil {
				return err
			}
		}
		if len(page) < walkPage {
			return nil
		}
		next, ok := successor(page[len(page)-1].ID)
		if !ok {
			return nil
		}
		start = next
	}
}

// successor returns the shard id that follows id in key order, or false when
// id is the largest possible id.
func successor(id ids.ShardID) (ids.ShardID, bool) {
	raw := id.AppendBinary(nil)
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] != 0xff {
			raw[i]++
			for j := i + 1; j < len(raw); j++ {
				raw[j] = 0
			}
			next, err := ids.ParseShardIDBinary(raw)
			return next, err == nil
		}
	}
	return ids.ShardID{}, false
}

// Segments returns the metadata of every segment the index knows.
func (x *Index) Segments() (map[uint64]SegmentMeta, error) {
	out := make(map[uint64]SegmentMeta)
	err := x.read(func(db *pebble.DB) error {
		iter, err := db.NewIter(&pebble.IterOptions{
			LowerBound: []byte{prefixSegment},
			UpperBound: []byte{prefixSegment + 1},
		})
		if err != nil {
			return fmt.Errorf("index: iterate segments: %w", err)
		}
		defer iter.Close()
		for ok := iter.First(); ok; ok = iter.Next() {
			k := iter.Key()
			if len(k) != 1+8 {
				return fmt.Errorf("index: malformed segment key of %d bytes", len(k))
			}
			seg := binary.BigEndian.Uint64(k[1:])
			m, err := decodeSegmentMeta(iter.Value())
			if err != nil {
				return fmt.Errorf("index: segment %d: %w", seg, err)
			}
			out[seg] = m
		}
		if err := iter.Error(); err != nil {
			return fmt.Errorf("index: iterate segments: %w", err)
		}
		return nil
	})
	return out, err
}

// Sync makes every committed batch durable, including batches committed with
// sync disabled.
func (x *Index) Sync() error {
	return x.read(func(db *pebble.DB) error {
		if err := db.LogData(nil, pebble.Sync); err != nil {
			return syncerr.Wrap("index wal sync", x.dir, err)
		}
		return nil
	})
}

// Batch returns an empty batch for this index. A batch should be committed or
// discarded before the index is closed.
func (x *Index) Batch() *Batch {
	return &Batch{x: x}
}

// Close closes the index. Calls after the first return ErrClosed.
func (x *Index) Close() error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed {
		return ErrClosed
	}
	x.closed = true
	if err := x.db.Close(); err != nil {
		return fmt.Errorf("index: close: %w", err)
	}
	return nil
}

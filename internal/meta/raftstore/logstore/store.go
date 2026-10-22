package logstore

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
	"github.com/eukalpia/unio/internal/ids"
)

// quietLogger is the pebble.Logger of every store. It drops informational
// messages and forwards errors and fatal messages to Pebble's default logger.
type quietLogger struct{}

// Infof implements pebble.Logger by dropping the message.
func (quietLogger) Infof(string, ...any) {}

// Errorf implements pebble.Logger.
func (quietLogger) Errorf(format string, args ...any) {
	pebble.DefaultLogger.Errorf(format, args...)
}

// Fatalf implements pebble.Logger. Pebble calls it on an invariant violation.
func (quietLogger) Fatalf(format string, args ...any) {
	pebble.DefaultLogger.Fatalf(format, args...)
}

// closedError is the error returned by every operation after Store.Close.
type closedError string

// Error implements the error interface.
func (e closedError) Error() string { return string(e) }

// ErrClosed is returned by every operation on a Store or GroupStore after
// Store.Close has been called. Test for it with errors.Is.
const ErrClosed closedError = "logstore: store is closed"

// Store holds the Raft logs and stable state of every group on one node in
// one Pebble database. Use Group to get the store of one group.
//
// A Store is safe for concurrent use. Close waits for operations in flight
// and then closes the database.
type Store struct {
	db *pebble.DB

	// mu is held for reading by every operation for its whole duration and
	// for writing by Close, so Close never closes the database under a
	// running operation.
	mu     sync.RWMutex
	closed bool

	// groupsMu guards groups. Each id maps to one GroupStore, which is the
	// only holder of that group's cached bounds.
	groupsMu sync.Mutex
	groups   map[ids.PartitionID]*GroupStore
}

// Open opens or creates the store in dir. cache is the Pebble block cache
// the database uses; it may be nil, in which case Pebble creates its own. The
// caller owns cache: Close does not release it, and the caller may keep using
// it for another open store.
//
// Open scans the database once to find every group that has keys and
// rebuilds the cached first and last index of each. It fails if the database
// holds a key that this store never writes.
//
// The store uses the plain OS filesystem and does not enable Pebble's
// disk-health monitor. That monitor starts a goroutine which is not stopped
// when Open fails, and this store has no handler for its slow-disk events.
// Pebble's informational log messages are dropped; errors still go to the
// standard logger.
func Open(dir string, cache *pebble.Cache) (*Store, error) {
	if dir == "" {
		return nil, errors.New("logstore: open: empty directory")
	}
	db, err := pebble.Open(dir, &pebble.Options{
		Cache:  cache,
		FS:     vfs.Default,
		Logger: quietLogger{},
	})
	if err != nil {
		return nil, fmt.Errorf("logstore: open %s: %w", dir, err)
	}
	s := &Store{db: db, groups: make(map[ids.PartitionID]*GroupStore)}
	if err := s.loadGroups(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("logstore: open %s: %w", dir, err)
	}
	return s, nil
}

// Group returns the store of group id. Calls with the same id return the same
// *GroupStore, so they share state. Group does no I/O: Open has already
// loaded every group that has keys, and a group Open did not find has none.
func (s *Store) Group(id ids.PartitionID) *GroupStore {
	s.groupsMu.Lock()
	defer s.groupsMu.Unlock()
	g, ok := s.groups[id]
	if !ok {
		g = &GroupStore{store: s, id: id}
		s.groups[id] = g
	}
	return g
}

// Close closes the database. Operations that started before Close finish
// first; later operations return ErrClosed. Close returns nil when the store
// is already closed.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("logstore: close: %w", err)
	}
	return nil
}

// enter admits one operation. The caller must call the returned function
// when the operation is done. enter must not be called while another enter
// is held by the same goroutine, because a pending Close would deadlock it.
func (s *Store) enter() (func(), error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrClosed
	}
	return s.mu.RUnlock, nil
}

// loadGroups finds every group with keys and rebuilds its bounds.
func (s *Store) loadGroups() error {
	found, err := s.scanGroups()
	if err != nil {
		return err
	}
	for _, id := range found {
		g, err := s.loadGroup(id)
		if err != nil {
			return err
		}
		s.groups[id] = g
	}
	return nil
}

// scanGroups returns the ids of all groups with at least one key. It jumps
// from one group to the next instead of reading every key.
func (s *Store) scanGroups() ([]ids.PartitionID, error) {
	it, err := s.db.NewIter(nil)
	if err != nil {
		return nil, fmt.Errorf("scan groups: %w", err)
	}
	var found []ids.PartitionID
	scanErr := func() error {
		for ok := it.First(); ok; {
			id, err := parseKey(it.Key())
			if err != nil {
				return err
			}
			found = append(found, id)
			if id == math.MaxUint32 {
				return nil
			}
			ok = it.SeekGE(groupStart(id + 1))
		}
		return it.Error()
	}()
	if err := it.Close(); err != nil && scanErr == nil {
		scanErr = fmt.Errorf("scan groups: %w", err)
	}
	if scanErr != nil {
		return nil, scanErr
	}
	return found, nil
}

// loadGroup builds the GroupStore of id from the database.
func (s *Store) loadGroup(id ids.PartitionID) (*GroupStore, error) {
	lower, upper := tagBounds(id, tagLog)
	first, err := s.edgeIndex(lower, upper, false)
	if err != nil {
		return nil, fmt.Errorf("load group %d: %w", id, err)
	}
	last, err := s.edgeIndex(lower, upper, true)
	if err != nil {
		return nil, fmt.Errorf("load group %d: %w", id, err)
	}
	g := &GroupStore{store: s, id: id}
	g.first.Store(first)
	g.last.Store(last)
	return g, nil
}

// edgeIndex returns the lowest (or, if highest is set, the highest) log index
// stored in [lower, upper), or 0 when there is none. lower and upper must be
// log key bounds of one group.
func (s *Store) edgeIndex(lower, upper []byte, highest bool) (uint64, error) {
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return 0, fmt.Errorf("index scan: %w", err)
	}
	var index uint64
	ok := it.First()
	if highest {
		ok = it.Last()
	}
	if ok {
		_, idx, perr := parseLogKey(it.Key())
		if perr != nil {
			_ = it.Close()
			return 0, perr
		}
		index = idx
	}
	if err := it.Error(); err != nil {
		_ = it.Close()
		return 0, fmt.Errorf("index scan: %w", err)
	}
	if err := it.Close(); err != nil {
		return 0, fmt.Errorf("index scan: %w", err)
	}
	return index, nil
}

// lowestAbove returns the lowest log index of group id that is greater than
// after, or 0 when there is none.
func (s *Store) lowestAbove(id ids.PartitionID, after uint64) (uint64, error) {
	if after == math.MaxUint64 {
		return 0, nil
	}
	_, upper := tagBounds(id, tagLog)
	return s.edgeIndex(logKey(id, after+1), upper, false)
}

// highestBelow returns the highest log index of group id that is less than
// before, or 0 when there is none.
func (s *Store) highestBelow(id ids.PartitionID, before uint64) (uint64, error) {
	if before == 0 {
		return 0, nil
	}
	lower, _ := tagBounds(id, tagLog)
	return s.edgeIndex(lower, logKey(id, before), true)
}

package local

import (
	"context"
	"path/filepath"
	"sync"
)

// formatLock is held by one caller at a time for one format path. The slot
// channel has room for one token, and holding the lock means holding it.
type formatLock struct {
	slot chan struct{}
	refs int // callers holding or waiting for the lock, guarded by lockTable.mu
}

// lockTable maps format paths to their locks. An entry exists only while
// some caller holds or waits for its lock.
type lockTable struct {
	mu    sync.Mutex
	locks map[string]*formatLock
}

// formatLocks serializes the writers of format records in this process. Each
// path has its own lock, so one slow disk does not hold up the others.
var formatLocks = lockTable{locks: make(map[string]*formatLock)}

// lockFormat acquires the format lock of path and returns the function that
// releases it. It gives up with the context's error if ctx is done before the
// lock is acquired.
func lockFormat(ctx context.Context, path string) (func(), error) {
	key, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	t := &formatLocks
	t.mu.Lock()
	l := t.locks[key]
	if l == nil {
		l = &formatLock{slot: make(chan struct{}, 1)}
		t.locks[key] = l
	}
	l.refs++
	t.mu.Unlock()

	select {
	case l.slot <- struct{}{}:
		return func() {
			<-l.slot
			t.drop(key, l)
		}, nil
	case <-ctx.Done():
		t.drop(key, l)
		return nil, ctx.Err()
	}
}

// drop releases one reference to l and forgets the lock when none remain.
func (t *lockTable) drop(key string, l *formatLock) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l.refs--
	if l.refs == 0 {
		delete(t.locks, key)
	}
}

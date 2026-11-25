package local

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockFormatWaitHonoursContext(t *testing.T) {
	key := filepath.Join(t.TempDir(), "format.pb")
	unlock, err := lockFormat(context.Background(), key)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := lockFormat(ctx, key); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock: err = %v, want DeadlineExceeded", err)
	}

	unlock()
	// The abandoned wait must not leave a reference behind: once the holder
	// releases, the lock table is empty again.
	formatLocks.mu.Lock()
	n := len(formatLocks.locks)
	formatLocks.mu.Unlock()
	if n != 0 {
		t.Fatalf("lock table holds %d entries after release, want 0", n)
	}
}

func TestLockFormatIsExclusivePerPath(t *testing.T) {
	dir := t.TempDir()
	a, err := lockFormat(context.Background(), filepath.Join(dir, "a.pb"))
	if err != nil {
		t.Fatalf("lock a: %v", err)
	}
	defer a()
	// A different path must not wait behind a.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b, err := lockFormat(ctx, filepath.Join(dir, "b.pb"))
	if err != nil {
		t.Fatalf("lock b while a is held: %v", err)
	}
	b()
}

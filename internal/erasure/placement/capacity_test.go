package placement

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// TestSetCapacityAccepts is the table for the eligibility rule: no online drive,
// the full ratio (inclusive), and the shard size against the emptiest drive.
func TestSetCapacityAccepts(t *testing.T) {
	cases := []struct {
		name  string
		c     setCapacity
		shard uint64
		ratio float64
		want  bool
	}{
		{"no online drive", setCapacity{online: 0, minFree: 1 << 40}, 0, 0.9, false},
		{"below the full ratio", setCapacity{online: 1, total: 1000, used: 899, minFree: 101}, 1, 0.9, true},
		{"at the full ratio", setCapacity{online: 1, total: 1000, used: 900, minFree: 100}, 1, 0.9, false},
		{"shard fits exactly", setCapacity{online: 2, total: 1000, minFree: 1000}, 1000, 0.9, true},
		{"shard one byte too big", setCapacity{online: 2, total: 1000, minFree: 1000}, 1001, 0.9, false},
		{"no capacity reported", setCapacity{online: 1, minFree: 10}, 10, 0.9, true},
		{"ratio of one leaves room below total", setCapacity{online: 1, total: 1000, used: 999, minFree: 1}, 1, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.accepts(tc.shard, tc.ratio); got != tc.want {
				t.Fatalf("accepts(%d, %v) = %v, want %v", tc.shard, tc.ratio, got, tc.want)
			}
		})
	}
}

// TestRefreshCountsHangingDriveOffline checks that a drive which never answers
// does not stall Refresh beyond its per-call timeout, and counts as offline.
func TestRefreshCountsHangingDriveOffline(t *testing.T) {
	f := newFixture(2, 2, 1, nil)
	hung := f.m.Sets[0].Drives[1]
	f.src[hung] = hangDrive{Drive: f.src[hung]}
	r := f.registry(t, Options{})

	start := time.Now()
	r.Refresh(context.Background())
	if elapsed := time.Since(start); elapsed > infoTimeout+time.Second {
		t.Fatalf("Refresh took %v with one hanging drive; the per-call timeout is %v", elapsed, infoTimeout)
	}
	snap := r.capacity.Load()
	if snap == nil {
		t.Fatal("Refresh published no snapshot")
	}
	if got := snap.sets[0].online; got != 2 {
		t.Fatalf("set 1 has %d online drives, want 2", got)
	}
	if got := snap.sets[1].online; got != 3 {
		t.Fatalf("set 2 has %d online drives, want 3", got)
	}
}

// TestRefreshKeepsSnapshotWhenCancelled checks that a refresh cut short by its
// caller publishes nothing. Otherwise every drive would look offline.
func TestRefreshKeepsSnapshotWhenCancelled(t *testing.T) {
	r := newFixture(2, 2, 1, nil).registry(t, Options{})
	r.Refresh(context.Background())
	before := r.capacity.Load()
	if before == nil {
		t.Fatal("Refresh published no snapshot")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Refresh(ctx)
	if r.capacity.Load() != before {
		t.Fatal("a cancelled refresh replaced the snapshot")
	}
}

// TestRunRefreshesUntilCancelled checks that Run refreshes on its interval and
// returns once its context is done, leaving no goroutine behind.
func TestRunRefreshesUntilCancelled(t *testing.T) {
	defer goleak.VerifyNone(t)
	f := newFixture(1, 2, 1, nil)
	var calls atomic.Int64
	for id, d := range f.src {
		f.src[id] = countingDrive{Drive: d, calls: func() { calls.Add(1) }}
	}
	r := f.registry(t, Options{RefreshInterval: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()

	// Each refresh asks the three drives of the set, so two refreshes make six
	// calls.
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 6 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got < 6 {
		t.Fatalf("Run made %d Info calls, want at least 6", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// TestSnapshotIsNilBeforeRefresh documents the starting state: no snapshot, so
// Place treats every set as eligible.
func TestSnapshotIsNilBeforeRefresh(t *testing.T) {
	r := newFixture(1, 2, 1, nil).registry(t, Options{})
	if r.capacity.Load() != nil {
		t.Fatal("a new registry has a snapshot before any refresh")
	}
}

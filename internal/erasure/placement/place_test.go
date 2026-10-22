package placement

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/s3err"
)

// countPlacements places n keys of bucket, each of size bytes, and counts the
// set that each one lands on.
func countPlacements(t *testing.T, r *Registry, bucket string, n int, size int64) map[ids.SetID]int {
	t.Helper()
	counts := make(map[ids.SetID]int)
	for i := range n {
		s, err := r.Place(context.Background(), bucket, fmt.Sprintf("key-%d", i), size)
		if err != nil {
			t.Fatalf("Place(key-%d): %v", i, err)
		}
		counts[s.ID()]++
	}
	return counts
}

// TestPlaceSpreadsKeysEvenly places 40000 keys over 8 sets. Each set must get
// its share within 20 percent, and every set must be used.
func TestPlaceSpreadsKeysEvenly(t *testing.T) {
	const sets, keys = 8, 40000
	r := newFixture(sets, 2, 1, nil).registry(t, Options{})
	counts := countPlacements(t, r, "photos", keys, 1024)
	if len(counts) != sets {
		t.Fatalf("keys reached %d sets, want %d", len(counts), sets)
	}
	want := keys / sets
	for id, n := range counts {
		if n < want*8/10 || n > want*12/10 {
			t.Errorf("set %d got %d keys, want %d plus or minus 20 percent", id, n, want)
		}
	}
}

// TestPlaceIsStable checks that the same bucket and key keep their set while
// capacity is unchanged: across refreshes, and in a registry built later from
// the same map.
func TestPlaceIsStable(t *testing.T) {
	ctx := context.Background()
	f := newFixture(8, 2, 1, nil)
	r := f.registry(t, Options{})
	first := make(map[string]ids.SetID)
	for i := range 500 {
		key := fmt.Sprintf("obj/%d", i)
		s, err := r.Place(ctx, "photos", key, 4096)
		if err != nil {
			t.Fatalf("Place(%s): %v", key, err)
		}
		first[key] = s.ID()
	}

	r.Refresh(ctx)
	restarted := f.registry(t, Options{})
	r.Refresh(ctx)
	for key, want := range first {
		for _, reg := range []*Registry{r, restarted} {
			s, err := reg.Place(ctx, "photos", key, 4096)
			if err != nil {
				t.Fatalf("Place(%s): %v", key, err)
			}
			if s.ID() != want {
				t.Fatalf("key %s moved from set %d to set %d with unchanged capacity", key, want, s.ID())
			}
		}
	}
}

// TestPlaceSkipsFullSets checks the full-set rule at its boundary. A set at
// exactly 90 percent used is full. A set just below it still takes keys.
func TestPlaceSkipsFullSets(t *testing.T) {
	rooms := map[ids.SetID]room{
		3: {total: 1000, used: 900, free: 100},
		4: {total: 1000, used: 899, free: 101},
	}
	r := newFixture(8, 2, 1, rooms).registry(t, Options{})
	r.Refresh(context.Background())
	counts := countPlacements(t, r, "b", 20000, 1)
	if counts[3] != 0 {
		t.Fatalf("full set 3 took %d keys", counts[3])
	}
	if counts[4] == 0 {
		t.Fatal("set 4 at 89.9 percent took no keys")
	}
}

// TestPlaceSkipsSetsTooSmallForObject checks the shard-size rule. With k=2, an
// object of 2001 bytes needs 1001 bytes per drive, which the two small sets
// cannot hold. An object of 2000 bytes needs exactly 1000, which they can.
func TestPlaceSkipsSetsTooSmallForObject(t *testing.T) {
	rooms := map[ids.SetID]room{
		1: {total: 1 << 40, free: 1000},
		2: {total: 1 << 40, free: 1000},
	}
	r := newFixture(4, 2, 1, rooms).registry(t, Options{})
	r.Refresh(context.Background())

	counts := countPlacements(t, r, "b", 4000, 2001)
	if counts[1] != 0 || counts[2] != 0 {
		t.Fatalf("sets too small for the shards took keys: %v", counts)
	}
	if counts[3] == 0 || counts[4] == 0 {
		t.Fatalf("roomy sets took no keys: %v", counts)
	}

	counts = countPlacements(t, r, "b", 4000, 2000)
	if counts[1] == 0 || counts[2] == 0 {
		t.Fatalf("sets with exactly enough room took no keys: %v", counts)
	}
}

// TestPlaceNoSpace checks that Place fails with ErrNoSpace, which classifies as
// InsufficientStorage, when every set is full or too small.
func TestPlaceNoSpace(t *testing.T) {
	cases := []struct {
		name  string
		rooms map[ids.SetID]room
		size  int64
	}{
		{"every set full", map[ids.SetID]room{
			1: {total: 1000, used: 1000, free: 0},
			2: {total: 1000, used: 950, free: 50},
		}, 1},
		{"every set too small", map[ids.SetID]room{
			1: {total: 1 << 40, free: 10},
			2: {total: 1 << 40, free: 10},
		}, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newFixture(2, 2, 1, tc.rooms).registry(t, Options{})
			r.Refresh(context.Background())
			_, err := r.Place(context.Background(), "b", "k", tc.size)
			if !errors.Is(err, ErrNoSpace) {
				t.Fatalf("Place: got %v, want ErrNoSpace", err)
			}
			if code := s3err.CodeOf(err); code != s3err.InsufficientStorage {
				t.Fatalf("code %q, want %q", code, s3err.InsufficientStorage)
			}
		})
	}
}

// TestPlaceBeforeRefreshAllowsEverySet checks that before the first refresh no
// capacity is known, so a full set is still eligible, and that the next
// refresh removes it.
func TestPlaceBeforeRefreshAllowsEverySet(t *testing.T) {
	rooms := map[ids.SetID]room{3: {total: 1000, used: 1000, free: 0}}
	r := newFixture(4, 2, 1, rooms).registry(t, Options{})
	if counts := countPlacements(t, r, "b", 4000, 1); counts[3] == 0 {
		t.Fatal("before the first refresh the full set took no keys")
	}
	r.Refresh(context.Background())
	if counts := countPlacements(t, r, "b", 4000, 1); counts[3] != 0 {
		t.Fatalf("after the refresh the full set took %d keys", counts[3])
	}
}

// TestPlaceSkipsSetWithNoOnlineDrive checks that a set whose drives are all
// offline is never chosen, even for an empty object.
func TestPlaceSkipsSetWithNoOnlineDrive(t *testing.T) {
	f := newFixture(3, 2, 1, nil)
	for _, id := range f.m.Sets[1].Drives {
		delete(f.src, id)
	}
	r := f.registry(t, Options{})
	r.Refresh(context.Background())
	if counts := countPlacements(t, r, "b", 3000, 0); counts[2] != 0 {
		t.Fatalf("set with no online drive took %d keys", counts[2])
	}
}

// TestPlaceFailsOnBadArguments checks that a negative size and a cancelled
// context are errors, and that neither is reported as a lack of space.
func TestPlaceFailsOnBadArguments(t *testing.T) {
	r := newFixture(2, 2, 1, nil).registry(t, Options{})
	if _, err := r.Place(context.Background(), "b", "k", -1); err == nil || errors.Is(err, ErrNoSpace) {
		t.Fatalf("negative size: got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Place(ctx, "b", "k", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: got %v, want context.Canceled", err)
	}
}

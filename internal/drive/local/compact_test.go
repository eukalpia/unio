package local

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/compact"
	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
	"github.com/eukalpia/unio/internal/ids"
)

// segmentFiles counts the segment files in dir.
func segmentFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".unio", "segments"))
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	return len(entries)
}

func TestCompactionReclaimsSegmentsAndKeepsLiveShards(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	d := openAt(t, dir, opts)
	ctx := context.Background()

	want := snapshot{}
	for i := 0; i < 200; i++ {
		data := payload(1000+i, i)
		if err := writeShard(d, idOf(i), data); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if i%5 == 0 {
			want[idOf(i)] = data
		}
	}
	for i := 0; i < 200; i++ {
		if i%5 != 0 {
			if err := d.DeleteShard(ctx, idOf(i)); err != nil {
				t.Fatalf("delete %d: %v", i, err)
			}
		}
	}
	before := segmentFiles(t, dir)

	retired, err := compact.Pass(ctx, compactTarget{d: d}, compact.Config{})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if retired == 0 {
		t.Fatal("no segment was retired")
	}
	if after := segmentFiles(t, dir); after >= before {
		t.Fatalf("segment files %d -> %d: compaction reclaimed nothing", before, after)
	}
	requireSnapshot(t, d, want)
	closeDrive(t, d)

	reopened := openAt(t, dir, opts)
	requireSnapshot(t, reopened, want)
	closeDrive(t, reopened)

	if err := os.RemoveAll(filepath.Join(dir, ".unio", "index")); err != nil {
		t.Fatal(err)
	}
	requireSnapshot(t, openAt(t, dir, opts), want)
}

// TestFailedCopyDuringCompactionKeepsData fails the index commit of a copy.
// The source segment must not be retired, and every shard must stay readable.
func TestFailedCopyDuringCompactionKeepsData(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	d := openAt(t, dir, opts)
	want := snapshot{}
	for i := 0; i < 120; i++ {
		data := payload(900, i)
		if err := writeShard(d, idOf(i), data); err != nil {
			t.Fatalf("write: %v", err)
		}
		if i%4 == 0 {
			want[idOf(i)] = data
		}
	}
	for i := 0; i < 120; i++ {
		if i%4 != 0 {
			if err := d.DeleteShard(context.Background(), idOf(i)); err != nil {
				t.Fatalf("delete: %v", err)
			}
		}
	}
	d.opts.hooks = failAt(faults.IndexCommit, 1)
	if _, err := compact.Pass(context.Background(), compactTarget{d: d}, compact.Config{}); err == nil {
		t.Fatal("pass succeeded although a copy failed")
	}
	requireSnapshot(t, d, want)
	closeDrive(t, d)
	requireSnapshot(t, openAt(t, dir, opts), want)
}

// TestModelWithCompactionAndRebuild runs random writes, deletes and reads
// against a model. Between phases it compacts, then restarts the drive: every
// other restart discards the index, so the state is recovered from the segment
// files alone. Tombstones must keep deleted shards deleted through both.
func TestModelWithCompactionAndRebuild(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	ctx := context.Background()
	d := openAt(t, dir, opts)

	rng := rand.New(rand.NewSource(42))
	model := snapshot{}
	var keys []ids.ShardID
	next := 1
	retired := 0
	for phase := 0; phase < 6; phase++ {
		for op := 0; op < 120; op++ {
			switch k := rng.Intn(10); {
			case k < 5 || len(keys) == 0:
				id := idOf(next)
				next++
				data := payload(rng.Intn(2*testPacked+1), next)
				if err := writeShard(d, id, data); err != nil {
					t.Fatalf("phase %d: write %s: %v", phase, id, err)
				}
				model[id] = data
				keys = append(keys, id)
			case k < 7:
				i := rng.Intn(len(keys))
				id := keys[i]
				keys[i] = keys[len(keys)-1]
				keys = keys[:len(keys)-1]
				if err := d.DeleteShard(ctx, id); err != nil {
					t.Fatalf("phase %d: delete %s: %v", phase, id, err)
				}
				delete(model, id)
			default:
				id := keys[rng.Intn(len(keys))]
				got, err := readShard(d, id)
				if err != nil || string(got) != string(model[id]) {
					t.Fatalf("phase %d: read %s: %v", phase, id, err)
				}
			}
		}
		n, err := compact.Pass(ctx, compactTarget{d: d}, compact.Config{})
		if err != nil {
			t.Fatalf("phase %d: compact: %v", phase, err)
		}
		retired += n
		requireSnapshot(t, d, model)

		closeDrive(t, d)
		if phase%2 == 1 {
			if err := os.RemoveAll(filepath.Join(dir, ".unio", "index")); err != nil {
				t.Fatal(err)
			}
		}
		d = openAt(t, dir, opts)
		requireSnapshot(t, d, model)
	}
	if retired == 0 {
		t.Fatal("compaction never retired a segment; the scenario does not exercise it")
	}
}

// TestDeletedShardStaysDeletedAfterRebuild exercises the tombstone rule
// directly: the record and its tombstone sit in different segments, the record's
// segment is compacted away, and the index is then rebuilt.
func TestDeletedShardStaysDeletedAfterRebuild(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	d := openAt(t, dir, opts)
	ctx := context.Background()

	victim := idOf(1)
	if err := writeShard(d, victim, payload(3000, 1)); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	// Fill the victim's segment with shards that will die, so it is compacted.
	for i := 2; i < 40; i++ {
		if err := writeShard(d, idOf(i), payload(1500, i)); err != nil {
			t.Fatalf("filler: %v", err)
		}
	}
	if err := d.DeleteShard(ctx, victim); err != nil {
		t.Fatalf("delete victim: %v", err)
	}
	for i := 2; i < 40; i++ {
		if err := d.DeleteShard(ctx, idOf(i)); err != nil {
			t.Fatalf("delete filler: %v", err)
		}
	}
	if _, err := compact.Pass(ctx, compactTarget{d: d}, compact.Config{}); err != nil {
		t.Fatalf("compact: %v", err)
	}
	closeDrive(t, d)

	if err := os.RemoveAll(filepath.Join(dir, ".unio", "index")); err != nil {
		t.Fatal(err)
	}
	d2 := openAt(t, dir, opts)
	if _, err := d2.StatShard(ctx, victim); !errors.Is(err, drive.ErrShardNotFound) {
		t.Fatalf("deleted shard came back after compaction and rebuild: %v", err)
	}
	requireSnapshot(t, d2, snapshot{})
}

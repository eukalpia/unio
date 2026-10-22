package drivetest

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

// suiteSizes are the shard sizes exercised by the round-trip case. They sit
// on both sides of the 4 KiB block size and the 1 MiB packing threshold.
var suiteSizes = []int64{0, 1, 4095, 4096, 4097, 1<<20 - 1, 1 << 20, 1<<20 + 1, 8 << 20}

// RunSuite runs the conformance suite against drives produced by open. open
// must return a new, empty drive for every call; the suite closes each drive
// through t.Cleanup. Every drive.Drive implementation must pass it.
func RunSuite(t *testing.T, open func(t *testing.T) drive.Drive) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, d drive.Drive)
	}{
		{"RoundTripSizes", caseRoundTrip},
		{"RangeReads", caseRangeReads},
		{"StatAndWalk", caseStatAndWalk},
		{"Delete", caseDelete},
		{"DuplicateID", caseDuplicate},
		{"Missing", caseMissing},
		{"SizeMismatch", caseSizeMismatch},
		{"ReaderError", caseReaderError},
		{"NegativeSize", caseNegativeSize},
		{"ConcurrentSameID", caseConcurrentSameID},
		{"ConcurrentDistinctIDs", caseConcurrentDistinct},
		{"ContextCancelled", caseContextCancelled},
		{"CloseSemantics", caseClose},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := open(t)
			t.Cleanup(func() { _ = d.Close() })
			tc.run(t, d)
		})
	}
}

// pattern returns n bytes whose values vary with position, so that shifted or
// misplaced bytes are detected.
func pattern(n int64, seed uint64) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((uint64(i)*0x9e3779b97f4a7c15 + seed) >> 56)
	}
	return b
}

// testID returns a deterministic shard id. Distinct n give distinct ids.
func testID(n int) ids.ShardID {
	var s ids.ShardID
	binary.BigEndian.PutUint64(s.Data[8:], uint64(n))
	s.Data[0] = 0x5a
	return s
}

func mustWrite(t *testing.T, d drive.Drive, id ids.ShardID, data []byte) {
	t.Helper()
	if err := d.WriteShard(context.Background(), id, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("write %s (%d bytes): %v", id, len(data), err)
	}
}

func readAll(t *testing.T, d drive.Drive, id ids.ShardID, off, length int64) []byte {
	t.Helper()
	rc, err := d.ReadShard(context.Background(), id, off, length)
	if err != nil {
		t.Fatalf("read %s [%d,+%d): %v", id, off, length, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read all %s: %v", id, err)
	}
	return b
}

func assertNotFound(t *testing.T, d drive.Drive, id ids.ShardID) {
	t.Helper()
	ctx := context.Background()
	if _, err := d.StatShard(ctx, id); !errors.Is(err, drive.ErrShardNotFound) {
		t.Fatalf("stat %s: err = %v, want ErrShardNotFound", id, err)
	}
	if rc, err := d.ReadShard(ctx, id, 0, -1); !errors.Is(err, drive.ErrShardNotFound) {
		if err == nil {
			rc.Close()
		}
		t.Fatalf("read %s: err = %v, want ErrShardNotFound", id, err)
	}
}

func caseRoundTrip(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	for i, size := range suiteSizes {
		id := testID(i + 1)
		want := pattern(size, uint64(i))
		mustWrite(t, d, id, want)

		info, err := d.StatShard(ctx, id)
		if err != nil {
			t.Fatalf("size %d: stat: %v", size, err)
		}
		if info.ID != id || info.Size != size {
			t.Fatalf("size %d: stat = %+v", size, info)
		}
		if got := readAll(t, d, id, 0, -1); !bytes.Equal(got, want) {
			t.Fatalf("size %d: read back differs", size)
		}
		if got := readAll(t, d, id, 0, size); !bytes.Equal(got, want) {
			t.Fatalf("size %d: explicit full-length read differs", size)
		}
	}
}

func caseRangeReads(t *testing.T, d drive.Drive) {
	const size = 1<<20 + 1
	id := testID(1)
	want := pattern(size, 7)
	mustWrite(t, d, id, want)

	cases := []struct {
		name   string
		off    int64
		length int64
	}{
		{"empty-at-start", 0, 0},
		{"first-byte", 0, 1},
		{"last-byte", size - 1, 1},
		{"empty-at-end", size, 0},
		{"middle", 1000, 4096},
		{"crosses-4k-edge", 4095, 2},
		{"crosses-1mib-edge", 1<<20 - 1, 2},
		{"to-end-negative", 5, -1},
		{"to-end-from-last", size - 1, -7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			end, err := ResolveRange(size, tc.off, tc.length)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			got := readAll(t, d, id, tc.off, tc.length)
			if !bytes.Equal(got, want[tc.off:end]) {
				t.Fatalf("range [%d,%d): got %d bytes that differ", tc.off, end, len(got))
			}
		})
	}

	invalid := []struct {
		name   string
		off    int64
		length int64
	}{
		{"negative-offset", -1, 1},
		{"offset-past-end", size + 1, -1},
		{"length-past-end", size - 1, 2},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			rc, err := d.ReadShard(context.Background(), id, tc.off, tc.length)
			if err == nil {
				rc.Close()
				t.Fatalf("read [%d,+%d): expected error", tc.off, tc.length)
			}
		})
	}
}

func caseStatAndWalk(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	want := map[ids.ShardID]int64{}
	for i, size := range []int64{0, 10, 5000} {
		id := testID(i + 1)
		mustWrite(t, d, id, pattern(size, 1))
		want[id] = size
	}
	for id, size := range want {
		info, err := d.StatShard(ctx, id)
		if err != nil || info.Size != size {
			t.Fatalf("stat %s = %+v, %v; want size %d", id, info, err, size)
		}
	}

	seen := map[ids.ShardID]int64{}
	if err := d.WalkShards(ctx, func(info drive.ShardInfo) error {
		if _, dup := seen[info.ID]; dup {
			return fmt.Errorf("walk visited %s twice", info.ID)
		}
		seen[info.ID] = info.Size
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(seen) != len(want) {
		t.Fatalf("walk saw %d shards, want %d", len(seen), len(want))
	}
	for id, size := range want {
		if seen[id] != size {
			t.Fatalf("walk: %s size %d, want %d", id, seen[id], size)
		}
	}

	stop := errors.New("stop walking")
	calls := 0
	err := d.WalkShards(ctx, func(drive.ShardInfo) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("walk stop: err = %v after %d calls", err, calls)
	}

	info, err := d.Info(ctx)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.ID != d.ID() || !info.Online {
		t.Fatalf("info = %+v", info)
	}
}

func caseDelete(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	id := testID(1)
	other := testID(2)
	mustWrite(t, d, id, pattern(4097, 3))
	mustWrite(t, d, other, pattern(2<<20, 4))

	for _, victim := range []ids.ShardID{id, other} {
		if err := d.DeleteShard(ctx, victim); err != nil {
			t.Fatalf("delete %s: %v", victim, err)
		}
		assertNotFound(t, d, victim)
		if err := d.DeleteShard(ctx, victim); !errors.Is(err, drive.ErrShardNotFound) {
			t.Fatalf("second delete %s: err = %v, want ErrShardNotFound", victim, err)
		}
	}

	// A deleted id may be written again.
	want := pattern(100, 9)
	mustWrite(t, d, id, want)
	if got := readAll(t, d, id, 0, -1); !bytes.Equal(got, want) {
		t.Fatal("rewrite after delete returned old bytes")
	}
}

func caseDuplicate(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	for _, size := range []int64{100, 2 << 20} {
		id := testID(int(size))
		original := pattern(size, 11)
		mustWrite(t, d, id, original)
		err := d.WriteShard(ctx, id, bytes.NewReader(pattern(size, 12)), size)
		if !errors.Is(err, drive.ErrShardExists) {
			t.Fatalf("size %d: duplicate err = %v, want ErrShardExists", size, err)
		}
		if got := readAll(t, d, id, 0, -1); !bytes.Equal(got, original) {
			t.Fatalf("size %d: failed duplicate write changed the shard", size)
		}
	}
}

func caseMissing(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	id := testID(99)
	assertNotFound(t, d, id)
	if err := d.DeleteShard(ctx, id); !errors.Is(err, drive.ErrShardNotFound) {
		t.Fatalf("delete missing: err = %v, want ErrShardNotFound", err)
	}
}

func caseSizeMismatch(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	for _, size := range []int64{100, 2 << 20} {
		short := testID(int(size) + 1)
		err := d.WriteShard(ctx, short, bytes.NewReader(pattern(size-1, 1)), size)
		if err == nil {
			t.Fatalf("size %d: short reader accepted", size)
		}
		assertNotFound(t, d, short)

		long := testID(int(size) + 2)
		err = d.WriteShard(ctx, long, bytes.NewReader(pattern(size+1, 1)), size)
		if err == nil {
			t.Fatalf("size %d: long reader accepted", size)
		}
		assertNotFound(t, d, long)
	}
}

// failingReader yields its prefix and then fails with a non-EOF error.
type failingReader struct {
	prefix []byte
	err    error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.prefix) == 0 {
		return 0, r.err
	}
	n := copy(p, r.prefix)
	r.prefix = r.prefix[n:]
	return n, nil
}

func caseReaderError(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	boom := errors.New("client went away")
	for _, size := range []int64{100, 3 << 20} {
		id := testID(int(size) + 3)
		r := &failingReader{prefix: pattern(size/2, 2), err: boom}
		err := d.WriteShard(ctx, id, r, size)
		if !errors.Is(err, boom) {
			t.Fatalf("size %d: err = %v, want the reader error", size, err)
		}
		assertNotFound(t, d, id)
	}
}

func caseNegativeSize(t *testing.T, d drive.Drive) {
	err := d.WriteShard(context.Background(), testID(1), bytes.NewReader(nil), -1)
	if err == nil {
		t.Fatal("negative size accepted")
	}
	assertNotFound(t, d, testID(1))
}

func caseConcurrentSameID(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	const writers = 100
	id := testID(7)
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		winners   [][]byte
		conflicts int
		failures  []error
	)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := pattern(int64(1000+i*37), uint64(i))
			err := d.WriteShard(ctx, id, bytes.NewReader(data), int64(len(data)))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, data)
			case errors.Is(err, drive.ErrShardExists):
				conflicts++
			default:
				failures = append(failures, err)
			}
		}(i)
	}
	wg.Wait()
	if len(failures) > 0 {
		t.Fatalf("unexpected errors: %v", failures[0])
	}
	if len(winners) != 1 || conflicts != writers-1 {
		t.Fatalf("winners %d, conflicts %d; want exactly one winner", len(winners), conflicts)
	}
	if got := readAll(t, d, id, 0, -1); !bytes.Equal(got, winners[0]) {
		t.Fatal("stored bytes are not the winner's bytes")
	}
}

func caseConcurrentDistinct(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	const writers = 100
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			size := int64(i*911%5000 + 1)
			if i%10 == 0 {
				size = 1<<20 + int64(i) // some blobs in the mix
			}
			errs <- d.WriteShard(ctx, testID(1000+i), bytes.NewReader(pattern(size, uint64(i))), size)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
	count := 0
	if err := d.WalkShards(ctx, func(drive.ShardInfo) error { count++; return nil }); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if count != writers {
		t.Fatalf("walk saw %d shards, want %d", count, writers)
	}
}

func caseContextCancelled(t *testing.T, d drive.Drive) {
	mustWrite(t, d, testID(1), pattern(10, 1))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := testID(2)
	if err := d.WriteShard(ctx, id, bytes.NewReader(pattern(10, 2)), 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("write with cancelled ctx: err = %v", err)
	}
	assertNotFound(t, d, id)
	if _, err := d.ReadShard(ctx, testID(1), 0, -1); !errors.Is(err, context.Canceled) {
		t.Fatalf("read with cancelled ctx: err = %v", err)
	}
	if _, err := d.StatShard(ctx, testID(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("stat with cancelled ctx: err = %v", err)
	}
	if err := d.DeleteShard(ctx, testID(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete with cancelled ctx: err = %v", err)
	}
	if err := d.WalkShards(ctx, func(drive.ShardInfo) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("walk with cancelled ctx: err = %v", err)
	}
	// The shard written before cancellation is untouched.
	if got := readAll(t, d, testID(1), 0, -1); !bytes.Equal(got, pattern(10, 1)) {
		t.Fatal("cancelled operations changed existing data")
	}
}

func caseClose(t *testing.T, d drive.Drive) {
	ctx := context.Background()
	id := testID(1)
	mustWrite(t, d, id, pattern(64, 5))
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := d.Close(); !errors.Is(err, drive.ErrClosed) {
		t.Fatalf("second close: err = %v, want ErrClosed", err)
	}
	if err := d.WriteShard(ctx, testID(2), bytes.NewReader(pattern(8, 1)), 8); !errors.Is(err, drive.ErrClosed) {
		t.Fatalf("write after close: err = %v", err)
	}
	if _, err := d.ReadShard(ctx, id, 0, -1); !errors.Is(err, drive.ErrClosed) {
		t.Fatalf("read after close: err = %v", err)
	}
	if _, err := d.StatShard(ctx, id); !errors.Is(err, drive.ErrClosed) {
		t.Fatalf("stat after close: err = %v", err)
	}
	if err := d.DeleteShard(ctx, id); !errors.Is(err, drive.ErrClosed) {
		t.Fatalf("delete after close: err = %v", err)
	}
	if err := d.WalkShards(ctx, func(drive.ShardInfo) error { return nil }); !errors.Is(err, drive.ErrClosed) {
		t.Fatalf("walk after close: err = %v", err)
	}
	if _, err := d.Info(ctx); !errors.Is(err, drive.ErrClosed) {
		t.Fatalf("info after close: err = %v", err)
	}
}

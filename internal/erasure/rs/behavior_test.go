package rs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/erasure/erasuretest"
	"github.com/eukalpia/unio/internal/ids"
)

// newTestSet returns a set over fresh in-memory drives and the drives.
func newTestSet(t *testing.T, k, m int, opts Options) (*Set, []*erasuretest.Drive) {
	t.Helper()
	mem := make([]*erasuretest.Drive, k+m)
	drives := make([]drive.Drive, k+m)
	for i := range mem {
		mem[i] = erasuretest.NewDrive()
		drives[i] = mem[i]
	}
	s, err := New(ids.SetID(9), drives, k, m, opts)
	if err != nil {
		t.Fatalf("New(%d+%d): %v", k, m, err)
	}
	t.Cleanup(func() {
		for _, d := range mem {
			_ = d.Close()
		}
	})
	return s, mem
}

// testCtx bounds an operation so that a regression fails instead of hanging.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// pseudo returns n deterministic bytes.
func pseudo(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(rng.Uint32())
	}
	return buf
}

func mustWrite(t *testing.T, s *Set, data []byte) (ids.DataID, erasure.Layout) {
	t.Helper()
	id := ids.NewDataID()
	layout, err := s.Write(testCtx(t), id, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Write %d bytes: %v", len(data), err)
	}
	return id, layout
}

func mustReadAll(t *testing.T, s *Set, id ids.DataID, layout erasure.Layout, size, off, length int64) []byte {
	t.Helper()
	rc, err := s.Read(testCtx(t), id, layout, size, off, length)
	if err != nil {
		t.Fatalf("Read [%d,+%d): %v", off, length, err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll [%d,+%d): %v", off, length, err)
	}
	return got
}

func TestWriteSkipsStalledDrive(t *testing.T) {
	s, mem := newTestSet(t, 2, 1, Options{StallTimeout: 20 * time.Millisecond})
	mem[0].SetFaults(erasuretest.Faults{Hang: true})
	data := pseudo(3*erasure.DefaultStripeSize+5, 1)
	id, layout := mustWrite(t, s, data)
	if !layout.Drives[0].IsZero() {
		t.Fatal("stalled drive is recorded as holding its shard")
	}
	if layout.Written() != 2 {
		t.Fatalf("Written = %d, want 2", layout.Written())
	}
	if got := mustReadAll(t, s, id, layout, int64(len(data)), 0, int64(len(data))); !bytes.Equal(got, data) {
		t.Fatal("object read back after a stalled write differs")
	}
}

func TestWriteFailsWhenStalledDrivesBreakQuorum(t *testing.T) {
	s, mem := newTestSet(t, 2, 2, Options{StallTimeout: 20 * time.Millisecond})
	mem[0].SetFaults(erasuretest.Faults{Hang: true})
	mem[1].SetFaults(erasuretest.Faults{Hang: true})
	_, err := s.Write(testCtx(t), ids.NewDataID(), bytes.NewReader(pseudo(100, 2)), 100)
	if !errors.Is(err, erasure.ErrWriteQuorum) {
		t.Fatalf("Write: err = %v, want ErrWriteQuorum", err)
	}
	for i, d := range mem {
		if d.ShardCount() != 0 {
			t.Fatalf("drive %d holds a shard after a failed write", i)
		}
	}
}

func TestReadQueuesDamagedShard(t *testing.T) {
	s, mem := newTestSet(t, 2, 1, Options{})
	data := pseudo(2*erasure.DefaultStripeSize, 3)
	id, layout := mustWrite(t, s, data)
	if err := mem[1].Corrupt(shardID(id, 1), 7); err != nil {
		t.Fatal(err)
	}
	if got := mustReadAll(t, s, id, layout, int64(len(data)), 0, int64(len(data))); !bytes.Equal(got, data) {
		t.Fatal("read with a corrupt data shard returned wrong bytes")
	}
	q := s.HealQueue()
	if q.Len() != 1 {
		t.Fatalf("heal queue holds %d requests, want 1", q.Len())
	}
	req, err := q.Pop(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if req.Shard != 1 || req.Data != id || req.Size != int64(len(data)) || req.Layout.Set != layout.Set {
		t.Fatalf("heal request = %+v, want shard 1 of %s", req, id)
	}
}

func TestHealQueueOverflowIsCounted(t *testing.T) {
	s, mem := newTestSet(t, 2, 2, Options{HealQueueSize: 1})
	data := pseudo(erasure.DefaultStripeSize+9, 4)
	id, layout := mustWrite(t, s, data)
	for _, i := range []int{0, 1} {
		if err := mem[i].Corrupt(shardID(id, i), 7); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustReadAll(t, s, id, layout, int64(len(data)), 0, int64(len(data))); !bytes.Equal(got, data) {
		t.Fatal("read with two corrupt data shards returned wrong bytes")
	}
	if s.HealQueue().Len() != 1 || s.HealQueue().Dropped() != 1 {
		t.Fatalf("queue len=%d dropped=%d, want 1 and 1", s.HealQueue().Len(), s.HealQueue().Dropped())
	}
}

func TestBudgetReturnsToZero(t *testing.T) {
	budget := bufpool.NewBudget(64 << 20)
	s, mem := newTestSet(t, 2, 1, Options{Budget: budget})
	data := pseudo(3*erasure.DefaultStripeSize+1, 5)
	size := int64(len(data))
	ctx := testCtx(t)

	id, layout := mustWrite(t, s, data)
	if budget.InUse() != 0 {
		t.Fatalf("after Write: %d bytes still in use", budget.InUse())
	}

	// A read closed before it is consumed must still release its frames.
	rc, err := s.Read(ctx, id, layout, size, 0, size)
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if budget.InUse() != 0 {
		t.Fatalf("after closing an unread Read: %d bytes in use", budget.InUse())
	}

	if _, err := s.Write(ctx, ids.NewDataID(), bytes.NewReader(data[:10]), 1000); err == nil {
		t.Fatal("Write accepted a short input")
	}
	if budget.InUse() != 0 {
		t.Fatalf("after a failed Write: %d bytes in use", budget.InUse())
	}

	// Heal a damaged shard, then delete with one drive offline; Delete
	// removes the object, so it must come last.
	if err := mem[0].Corrupt(shardID(id, 0), 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heal(ctx, id, layout, size); err != nil {
		t.Fatalf("Heal: %v", err)
	}
	if budget.InUse() != 0 {
		t.Fatalf("after Heal: %d bytes in use", budget.InUse())
	}
	mem[0].SetFaults(erasuretest.Faults{Offline: true})
	if err := s.Delete(ctx, id, layout); err == nil {
		t.Fatal("Delete with an offline drive returned nil")
	}
	if budget.InUse() != 0 {
		t.Fatalf("after a failed Delete: %d bytes in use", budget.InUse())
	}
}

func TestSmallBudgetCompletesLargeObject(t *testing.T) {
	const stripe = 64 << 10
	// Room for about five frames of a 2+1 set with 64 KiB stripes.
	budget := bufpool.NewBudget(5 * 3 * (stripe / 2))
	s, _ := newTestSet(t, 2, 1, Options{StripeSize: stripe, Budget: budget})
	data := pseudo(8<<20+3, 6)
	id, layout := mustWrite(t, s, data)
	if got := mustReadAll(t, s, id, layout, int64(len(data)), 0, int64(len(data))); !bytes.Equal(got, data) {
		t.Fatal("large object read back with a small budget differs")
	}
	if budget.InUse() != 0 {
		t.Fatalf("%d bytes still in use", budget.InUse())
	}
}

// TestSmallStripes exercises block boundaries that the 1 MiB suite does not
// reach: a stripe size that does not divide evenly by k, short last stripes,
// and reconstruction of partial stripes.
func TestSmallStripes(t *testing.T) {
	shapes := []struct{ k, m int }{{1, 0}, {2, 1}, {3, 2}, {5, 3}}
	sizes := []int64{0, 1, 999, 1000, 1001, 2999, 3000, 3001, 12345}
	for _, sh := range shapes {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%d+%d/size=%d", sh.k, sh.m, size), func(t *testing.T) {
				s, mem := newTestSet(t, sh.k, sh.m, Options{StripeSize: 1000})
				data := pseudo(int(size), uint64(size))
				id, layout := mustWrite(t, s, data)
				rng := rand.New(rand.NewPCG(uint64(size), 7))
				for range 40 {
					off := int64(0)
					n := size
					if size > 0 {
						off = rng.Int64N(size)
						n = rng.Int64N(size - off + 1)
					}
					if got := mustReadAll(t, s, id, layout, size, off, n); !bytes.Equal(got, data[off:off+n]) {
						t.Fatalf("range [%d,+%d) differs", off, n)
					}
				}
				// Take the drive of the last data shard offline: every stripe
				// must be rebuilt from parity.
				if sh.m > 0 && size > 0 {
					mem[sh.k-1].SetFaults(erasuretest.Faults{Offline: true})
					if got := mustReadAll(t, s, id, layout, size, 0, size); !bytes.Equal(got, data) {
						t.Fatal("reconstructed read differs")
					}
				}
			})
		}
	}
}

func TestLayoutMismatchIsRejected(t *testing.T) {
	s, _ := newTestSet(t, 2, 1, Options{})
	id, layout := mustWrite(t, s, pseudo(10, 8))
	other := layout
	other.Set = ids.SetID(99)
	wrongShape := layout
	wrongShape.ParityShards = 2
	wrongShape.Drives = append(append([]ids.DriveID(nil), layout.Drives...), ids.DriveID{1})
	tests := []struct {
		name   string
		layout erasure.Layout
	}{
		{"other set", other},
		{"other shape", wrongShape},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.Read(testCtx(t), id, tt.layout, 10, 0, 10); err == nil {
				t.Fatal("Read accepted a foreign layout")
			}
			if err := s.Delete(testCtx(t), id, tt.layout); err == nil {
				t.Fatal("Delete accepted a foreign layout")
			}
			if _, err := s.Heal(testCtx(t), id, tt.layout, 10); err == nil {
				t.Fatal("Heal accepted a foreign layout")
			}
		})
	}
}

func TestReadRejectsOutOfRangeRequests(t *testing.T) {
	s, _ := newTestSet(t, 2, 1, Options{})
	id, layout := mustWrite(t, s, pseudo(10, 9))
	for _, r := range [][2]int64{{-1, 1}, {0, -1}, {5, 6}, {11, 0}} {
		if _, err := s.Read(testCtx(t), id, layout, 10, r[0], r[1]); err == nil {
			t.Fatalf("Read [%d,+%d) of 10 bytes accepted", r[0], r[1])
		}
	}
}

func TestWriteRejectsNegativeSize(t *testing.T) {
	s, _ := newTestSet(t, 2, 1, Options{})
	if _, err := s.Write(testCtx(t), ids.NewDataID(), bytes.NewReader(nil), -1); err == nil {
		t.Fatal("negative size accepted")
	}
}

func TestBlockGeometryMatchesBitrot(t *testing.T) {
	s, _ := newTestSet(t, 3, 2, Options{StripeSize: 1000})
	g, err := s.geomFor(1000)
	if err != nil {
		t.Fatal(err)
	}
	if g.block != bitrot.ShardBlockSize(1000, 3) {
		t.Fatalf("block = %d, want %d", g.block, bitrot.ShardBlockSize(1000, 3))
	}
}

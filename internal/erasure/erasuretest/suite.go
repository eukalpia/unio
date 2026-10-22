package erasuretest

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// SetFactory builds the erasure set under test over drives, where drive i
// must hold shard i. It is called once per subtest with fresh drives.
type SetFactory func(t *testing.T, drives []drive.Drive, k, m int) erasure.Set

// suiteShapes are the data and parity shard counts the suite exercises.
var suiteShapes = []struct{ k, m int }{
	{1, 0}, {2, 1}, {2, 2}, {4, 2}, {8, 4}, {12, 4},
}

const stripeSize = int64(erasure.DefaultStripeSize)

// corruptOffset is the byte flipped by corruption faults. It lies in the
// first block of every shard the suite writes.
const corruptOffset = 7

// midStreamBytes is how far into a shard a mid-stream write fault fires.
const midStreamBytes = 1000

// RunSetSuite runs the conformance suite against the sets built by newSet.
// Every erasure.Set implementation must pass it. Under -short the 100 MiB
// round trip is skipped.
func RunSetSuite(t *testing.T, newSet SetFactory) {
	t.Helper()
	for _, sh := range suiteShapes {
		t.Run(fmt.Sprintf("%d+%d", sh.k, sh.m), func(t *testing.T) {
			for _, c := range suiteCases {
				t.Run(c.name, func(t *testing.T) {
					c.run(t, newSet, sh.k, sh.m)
				})
			}
		})
	}
}

// suiteCase is one conformance test run for one shape.
type suiteCase struct {
	name string
	run  func(t *testing.T, newSet SetFactory, k, m int)
}

var suiteCases = []suiteCase{
	{"RoundTrip", testRoundTrip},
	{"RangeReads", testRangeReads},
	{"ReadWithFailedDrives", testReadWithFailedDrives},
	{"WriteWithFailedDrives", testWriteWithFailedDrives},
	{"WriteFailsMidStream", testWriteFailsMidStream},
	{"InputErrorAbortsWrite", testInputErrorAbortsWrite},
	{"InputSizeMismatch", testInputSizeMismatch},
	{"CancelledWrite", testCancelledWrite},
	{"CancelledRead", testCancelledRead},
	{"Delete", testDelete},
	{"DeleteReportsOfflineDrive", testDeleteReportsOfflineDrive},
	{"HealHealthyObject", testHealHealthyObject},
	{"HealRestoresCorruptShard", testHealRestoresCorruptShard},
	{"HealRewritesMissingShards", testHealRewritesMissingShards},
	{"HealBeyondParityFails", testHealBeyondParityFails},
	{"MisdirectedShard", testMisdirectedShard},
}

// fixture is one erasure set over fresh in-memory drives.
type fixture struct {
	newSet SetFactory
	k, m   int
	mem    []*Drive
	set    erasure.Set
}

func newFixture(t *testing.T, newSet SetFactory, k, m int) *fixture {
	t.Helper()
	f := &fixture{newSet: newSet, k: k, m: m, mem: make([]*Drive, k+m)}
	drives := make([]drive.Drive, k+m)
	for i := range f.mem {
		f.mem[i] = NewDrive()
		drives[i] = f.mem[i]
	}
	t.Cleanup(func() {
		for _, d := range f.mem {
			_ = d.Close()
		}
	})
	f.set = newSet(t, drives, k, m)
	return f
}

// write stores data under a new id.
func (f *fixture) write(ctx context.Context, data []byte) (ids.DataID, erasure.Layout, error) {
	id := ids.NewDataID()
	layout, err := f.set.Write(ctx, id, bytes.NewReader(data), int64(len(data)))
	return id, layout, err
}

// mustWrite stores data under a new id and fails the test on error.
func (f *fixture) mustWrite(t *testing.T, data []byte) (ids.DataID, erasure.Layout) {
	t.Helper()
	id, layout, err := f.write(context.Background(), data)
	if err != nil {
		t.Fatalf("Write %d bytes: %v", len(data), err)
	}
	return id, layout
}

// shardsStored returns the number of shards held across all drives.
func (f *fixture) shardsStored() int {
	n := 0
	for _, d := range f.mem {
		n += d.ShardCount()
	}
	return n
}

// readRange reads [offset, offset+length) of an object.
func (f *fixture) readRange(ctx context.Context, id ids.DataID, layout erasure.Layout, size, offset, length int64) ([]byte, error) {
	rc, err := f.set.Read(ctx, id, layout, size, offset, length)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// payload returns n deterministic pseudo-random bytes derived from seed.
func payload(n int64, seed uint64) []byte {
	var key [32]byte
	binary.LittleEndian.PutUint64(key[:], seed)
	buf := make([]byte, n)
	_, _ = rand.NewChaCha8(key).Read(buf)
	return buf
}

// failAfterReader yields data and then err in place of EOF.
type failAfterReader struct {
	data []byte
	err  error
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// cancelOnFirstRead cancels its context on the first Read, then reads from
// the underlying reader.
type cancelOnFirstRead struct {
	r      io.Reader
	cancel context.CancelFunc
	done   bool
}

func (c *cancelOnFirstRead) Read(p []byte) (int, error) {
	if !c.done {
		c.done = true
		c.cancel()
	}
	return c.r.Read(p)
}

// checkPlacement verifies that drive i holds exactly shard i of the object
// and that the layout records drive i's id.
func (f *fixture) checkPlacement(t *testing.T, id ids.DataID, layout erasure.Layout) {
	t.Helper()
	if layout.Written() != f.k+f.m {
		t.Fatalf("layout records %d shards, want %d", layout.Written(), f.k+f.m)
	}
	for i, d := range f.mem {
		if layout.Drives[i] != d.ID() {
			t.Fatalf("layout entry %d is %s, want drive id %s", i, layout.Drives[i], d.ID())
		}
		if _, ok := d.Shard(ids.ShardID{Data: id, Index: uint16(i)}); !ok {
			t.Fatalf("drive %d does not hold shard %d", i, i)
		}
	}
}

func testRoundTrip(t *testing.T, newSet SetFactory, k, m int) {
	sizes := []int64{0, 1, stripeSize - 1, stripeSize, stripeSize + 1, 10*stripeSize + 7}
	if !testing.Short() && k == 4 && m == 2 {
		sizes = append(sizes, 100<<20)
	}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			f := newFixture(t, newSet, k, m)
			data := payload(size, 1)
			id, layout := f.mustWrite(t, data)
			f.checkPlacement(t, id, layout)
			got, err := f.readRange(context.Background(), id, layout, size, 0, size)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Fatal("round trip returned different bytes")
			}
		})
	}
}

func testRangeReads(t *testing.T, newSet SetFactory, k, m int) {
	f := newFixture(t, newSet, k, m)
	size := 2*stripeSize + stripeSize/2
	data := payload(size, 42)
	id, layout := f.mustWrite(t, data)

	ranges := [][2]int64{
		{0, 0},
		{0, 1},
		{stripeSize - 1, 2},
		{stripeSize, 0},
		{stripeSize, stripeSize},
		{stripeSize - 1, stripeSize + 2},
		{size - 1, 1},
		{0, size},
		{2*stripeSize - 1, stripeSize/2 + 1},
	}
	rng := rand.New(rand.NewPCG(1, 2)) // #nosec G404 -- seeded PRNG picks test range offsets; not used for secrets
	for range 32 {
		off := rng.Int64N(size)
		ranges = append(ranges, [2]int64{off, rng.Int64N(size - off + 1)})
	}
	for _, r := range ranges {
		got, err := f.readRange(context.Background(), id, layout, size, r[0], r[1])
		if err != nil {
			t.Fatalf("Read [%d,+%d): %v", r[0], r[1], err)
		}
		if !bytes.Equal(got, data[r[0]:r[0]+r[1]]) {
			t.Fatalf("Read [%d,+%d) returned wrong bytes", r[0], r[1])
		}
	}
}

// injectFaults applies n faults: even-indexed drives go offline and
// odd-indexed drives get a corrupted byte in the stored shard.
func (f *fixture) injectFaults(t *testing.T, id ids.DataID, n int) {
	t.Helper()
	for i := range n {
		if i%2 == 0 {
			f.mem[i].SetFaults(Faults{Offline: true})
			continue
		}
		if err := f.mem[i].Corrupt(ids.ShardID{Data: id, Index: uint16(i)}, corruptOffset); err != nil {
			t.Fatal(err)
		}
	}
}

func testReadWithFailedDrives(t *testing.T, newSet SetFactory, k, m int) {
	size := 2*stripeSize + stripeSize/2
	t.Run("tolerates M faults", func(t *testing.T) {
		f := newFixture(t, newSet, k, m)
		data := payload(size, 7)
		id, layout := f.mustWrite(t, data)
		f.injectFaults(t, id, m)
		got, err := f.readRange(context.Background(), id, layout, size, 0, size)
		if err != nil {
			t.Fatalf("Read with %d faults: %v", m, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatal("read with faults returned wrong bytes")
		}
		got, err = f.readRange(context.Background(), id, layout, size, stripeSize-3, 9)
		if err != nil || !bytes.Equal(got, data[stripeSize-3:stripeSize+6]) {
			t.Fatalf("range read with faults: err=%v", err)
		}
	})
	t.Run("fails beyond M faults", func(t *testing.T) {
		f := newFixture(t, newSet, k, m)
		data := payload(size, 8)
		id, layout := f.mustWrite(t, data)
		for i := range m + 1 {
			f.mem[i].SetFaults(Faults{Offline: true})
		}
		_, err := f.readRange(context.Background(), id, layout, size, 0, size)
		if !errors.Is(err, erasure.ErrReadQuorum) {
			t.Fatalf("Read with %d offline drives: err = %v, want ErrReadQuorum", m+1, err)
		}
	})
}

// writeWithFailures makes the first n drives fail writes, either at once or
// after midStreamBytes bytes.
func writeWithFailures(t *testing.T, newSet SetFactory, k, m, n int, midStream bool) (*fixture, error) {
	t.Helper()
	f := newFixture(t, newSet, k, m)
	for i := range n {
		fault := Faults{WriteErr: drive.ErrFaulty}
		if midStream {
			// Shards of the test object are a few hundred bytes, so fail early.
			fault.WriteErrAfter = midStreamBytes
		}
		f.mem[i].SetFaults(fault)
	}
	data := payload(2*stripeSize+3, 11)
	id, layout, err := f.write(context.Background(), data)
	if err != nil {
		return f, err
	}
	if layout.Written() != k+m-n {
		t.Fatalf("layout records %d shards, want %d", layout.Written(), k+m-n)
	}
	for i := range n {
		if !layout.Drives[i].IsZero() {
			t.Fatalf("failed drive %d has non-zero layout entry", i)
		}
	}
	got, err := f.readRange(context.Background(), id, layout, int64(len(data)), 0, int64(len(data)))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read after %d failed writes: err=%v", n, err)
	}
	return f, nil
}

// tolerableWriteFailures is the number of drive write failures a write
// survives: total shards minus the write quorum.
func tolerableWriteFailures(k, m int) int {
	return k + m - erasure.Layout{DataShards: k, ParityShards: m}.WriteQuorum()
}

func testWriteWithFailedDrives(t *testing.T, newSet SetFactory, k, m int) {
	n := tolerableWriteFailures(k, m)
	t.Run("tolerated failures succeed", func(t *testing.T) {
		if _, err := writeWithFailures(t, newSet, k, m, n, false); err != nil {
			t.Fatalf("write with %d failed drives: %v", n, err)
		}
	})
	t.Run("one more failure fails with ErrWriteQuorum", func(t *testing.T) {
		f, err := writeWithFailures(t, newSet, k, m, n+1, false)
		if !errors.Is(err, erasure.ErrWriteQuorum) {
			t.Fatalf("write with %d failed drives: err = %v, want ErrWriteQuorum", n+1, err)
		}
		if got := f.shardsStored(); got != 0 {
			t.Fatalf("%d shards left behind after a failed write", got)
		}
	})
}

func testWriteFailsMidStream(t *testing.T, newSet SetFactory, k, m int) {
	n := tolerableWriteFailures(k, m)
	if _, err := writeWithFailures(t, newSet, k, m, n, true); err != nil {
		t.Fatalf("write with %d mid-stream failures: %v", n, err)
	}
}

func testInputErrorAbortsWrite(t *testing.T, newSet SetFactory, k, m int) {
	f := newFixture(t, newSet, k, m)
	injected := errors.New("client sent a bad checksum")
	src := &failAfterReader{data: payload(2*stripeSize+stripeSize/3, 5), err: injected}
	_, err := f.set.Write(context.Background(), ids.NewDataID(), src, 3*stripeSize)
	if !errors.Is(err, injected) {
		t.Fatalf("Write: err = %v, want the input error", err)
	}
	if got := f.shardsStored(); got != 0 {
		t.Fatalf("%d shards left behind after an input error", got)
	}
}

func testInputSizeMismatch(t *testing.T, newSet SetFactory, k, m int) {
	data := payload(stripeSize+5, 6)
	for _, size := range []int64{int64(len(data)) - 1, int64(len(data)) + 1} {
		t.Run(fmt.Sprintf("declared=%d", size), func(t *testing.T) {
			f := newFixture(t, newSet, k, m)
			_, err := f.set.Write(context.Background(), ids.NewDataID(), bytes.NewReader(data), size)
			if err == nil {
				t.Fatal("Write accepted a size that does not match the input")
			}
			if got := f.shardsStored(); got != 0 {
				t.Fatalf("%d shards left behind after a size mismatch", got)
			}
		})
	}
}

func testCancelledWrite(t *testing.T, newSet SetFactory, k, m int) {
	data := payload(3*stripeSize, 3)
	t.Run("already cancelled", func(t *testing.T) {
		f := newFixture(t, newSet, k, m)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := f.write(ctx, data)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Write: err = %v, want context.Canceled", err)
		}
		if got := f.shardsStored(); got != 0 {
			t.Fatalf("%d shards left behind", got)
		}
	})
	t.Run("cancelled mid-write", func(t *testing.T) {
		f := newFixture(t, newSet, k, m)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		src := &cancelOnFirstRead{r: bytes.NewReader(data), cancel: cancel}
		_, err := f.set.Write(ctx, ids.NewDataID(), src, int64(len(data)))
		if err == nil {
			t.Fatal("Write succeeded after cancellation")
		}
		if got := f.shardsStored(); got != 0 {
			t.Fatalf("%d shards left behind", got)
		}
	})
}

func testCancelledRead(t *testing.T, newSet SetFactory, k, m int) {
	f := newFixture(t, newSet, k, m)
	size := 20 * stripeSize
	id, layout := f.mustWrite(t, payload(size, 9))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := f.set.Read(ctx, id, layout, size, 0, size)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	defer rc.Close()
	if _, err := io.ReadFull(rc, make([]byte, 64<<10)); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	cancel()
	_, err = io.Copy(io.Discard, rc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancel: err = %v, want context.Canceled", err)
	}
}

func testDelete(t *testing.T, newSet SetFactory, k, m int) {
	f := newFixture(t, newSet, k, m)
	id, layout := f.mustWrite(t, payload(stripeSize+1, 13))
	ctx := context.Background()
	if err := f.set.Delete(ctx, id, layout); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.shardsStored(); got != 0 {
		t.Fatalf("%d shards survive Delete", got)
	}
	if err := f.set.Delete(ctx, id, layout); err != nil {
		t.Fatalf("Delete of a missing object: %v", err)
	}
}

func testDeleteReportsOfflineDrive(t *testing.T, newSet SetFactory, k, m int) {
	f := newFixture(t, newSet, k, m)
	id, layout := f.mustWrite(t, payload(stripeSize+1, 14))
	f.mem[0].SetFaults(Faults{Offline: true})
	if err := f.set.Delete(context.Background(), id, layout); err == nil {
		t.Fatal("Delete with an offline drive returned nil")
	}
	for i := 1; i < len(f.mem); i++ {
		if f.mem[i].ShardCount() != 0 {
			t.Fatalf("online drive %d still holds a shard", i)
		}
	}
}

func testHealHealthyObject(t *testing.T, newSet SetFactory, k, m int) {
	f := newFixture(t, newSet, k, m)
	id, layout := f.mustWrite(t, payload(2*stripeSize, 15))
	healed, err := f.set.Heal(context.Background(), id, layout, 2*stripeSize)
	if err != nil {
		t.Fatalf("Heal of a healthy object: %v", err)
	}
	if !equalLayouts(healed, layout) {
		t.Fatal("Heal changed the layout of a healthy object")
	}
}

func testHealRestoresCorruptShard(t *testing.T, newSet SetFactory, k, m int) {
	f := newFixture(t, newSet, k, m)
	size := 2*stripeSize + stripeSize/2
	data := payload(size, 16)
	id, layout := f.mustWrite(t, data)
	shard := ids.ShardID{Data: id, Index: 0}
	original, _ := f.mem[0].Shard(shard)
	if err := f.mem[0].Corrupt(shard, corruptOffset); err != nil {
		t.Fatal(err)
	}
	if m == 0 {
		// Without parity a corrupt shard cannot be rebuilt, so Read must fail
		// and Heal must report the damage.
		if _, err := f.readRange(context.Background(), id, layout, size, 0, size); err == nil {
			t.Fatal("read of a corrupt shard without parity succeeded")
		}
		if _, err := f.set.Heal(context.Background(), id, layout, size); err == nil {
			t.Fatal("Heal without parity reported success")
		}
		return
	}
	healed, err := f.set.Heal(context.Background(), id, layout, size)
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}
	if !equalLayouts(healed, layout) {
		t.Fatal("Heal changed the layout when rewriting an existing shard")
	}
	if got, _ := f.mem[0].Shard(shard); !bytes.Equal(got, original) {
		t.Fatal("healed shard differs from the original bytes")
	}
	got, err := f.readRange(context.Background(), id, layout, size, 0, size)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read after heal: err=%v", err)
	}
}

func testHealRewritesMissingShards(t *testing.T, newSet SetFactory, k, m int) {
	n := tolerableWriteFailures(k, m)
	f := newFixture(t, newSet, k, m)
	for i := range n {
		f.mem[i].SetFaults(Faults{WriteErr: drive.ErrFaulty})
	}
	size := int64(2*stripeSize + 3)
	data := payload(size, 11)
	id, layout, err := f.write(context.Background(), data)
	if err != nil {
		t.Fatalf("write with %d failed drives: %v", n, err)
	}
	for i := range n {
		f.mem[i].SetFaults(Faults{})
	}
	healed, err := f.set.Heal(context.Background(), id, layout, size)
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}
	if healed.Written() != k+m {
		t.Fatalf("after Heal %d shards are recorded, want %d", healed.Written(), k+m)
	}
	f.checkPlacement(t, id, healed)
	got, err := f.readRange(context.Background(), id, healed, size, 0, size)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read after heal: err=%v", err)
	}
}

func testHealBeyondParityFails(t *testing.T, newSet SetFactory, k, m int) {
	f := newFixture(t, newSet, k, m)
	size := 2 * stripeSize
	id, layout := f.mustWrite(t, payload(size, 17))
	for i := range m + 1 {
		if err := f.mem[i].Corrupt(ids.ShardID{Data: id, Index: uint16(i)}, corruptOffset); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.set.Heal(context.Background(), id, layout, size); err == nil {
		t.Fatalf("Heal with %d corrupt shards returned nil", m+1)
	}
}

// testMisdirectedShard puts a valid shard where it does not belong: a shard of
// another object at the same index, or another shard of the same object. Its
// bytes verify at the position they were written to, so only the position
// binding can reject them. A read must never return wrong bytes. With parity
// the damaged shard is rebuilt; without parity the read must fail.
func testMisdirectedShard(t *testing.T, newSet SetFactory, k, m int) {
	size := 2*stripeSize + 11
	cases := []struct {
		name        string
		otherObject bool // take shard 0 of another object, else shard 1 of this one
	}{
		{"shard of another object at the same index", true},
		{"another shard of the same object", false},
	}
	for _, c := range cases {
		if !c.otherObject && k+m < 2 {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, newSet, k, m)
			dataA := payload(size, 21)
			idA, layoutA := f.mustWrite(t, dataA)
			idB, _ := f.mustWrite(t, payload(size, 22))

			from := ids.ShardID{Data: idA, Index: 1}
			if c.otherObject {
				from = ids.ShardID{Data: idB, Index: 0}
			}
			bytesFrom, ok := f.mem[from.Index].Shard(from)
			if !ok {
				t.Fatalf("source shard %s not found", from)
			}
			f.mem[0].Replace(ids.ShardID{Data: idA, Index: 0}, bytesFrom)

			got, err := f.readRange(context.Background(), idA, layoutA, size, 0, size)
			if err == nil && !bytes.Equal(got, dataA) {
				t.Fatal("misdirected shard returned wrong bytes")
			}
			if m == 0 {
				if err == nil {
					t.Fatal("misdirected shard was served without parity to repair it")
				}
				return
			}
			if err != nil {
				t.Fatalf("read with a misdirected shard: %v", err)
			}
		})
	}
}

// equalLayouts reports whether two layouts record the same placement.
func equalLayouts(a, b erasure.Layout) bool {
	if a.Set != b.Set || a.DataShards != b.DataShards || a.ParityShards != b.ParityShards ||
		a.StripeSize != b.StripeSize || len(a.Drives) != len(b.Drives) {
		return false
	}
	for i := range a.Drives {
		if a.Drives[i] != b.Drives[i] {
			return false
		}
	}
	return true
}

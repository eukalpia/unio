package rs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/klauspost/reedsolomon"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/erasure/erasuretest"
	"github.com/eukalpia/unio/internal/ids"
)

// benchShapes are the shapes benchmarked; benchObject is the object size for
// the end-to-end benchmarks. Drives are in memory, so these measure the
// pipeline and its copies, not disks.
var benchShapes = []struct{ k, m int }{{2, 1}, {4, 2}, {8, 4}, {12, 4}}

const benchObject = 32 << 20

// benchSet returns a set with default options over in-memory drives.
func benchSet(b *testing.B, k, m int) (*Set, []*erasuretest.Drive) {
	b.Helper()
	mem := make([]*erasuretest.Drive, k+m)
	drives := make([]drive.Drive, k+m)
	for i := range mem {
		mem[i] = erasuretest.NewDrive()
		drives[i] = mem[i]
	}
	s, err := New(ids.SetID(3), drives, k, m, Options{})
	if err != nil {
		b.Fatal(err)
	}
	return s, mem
}

// BenchmarkEncode measures Reed-Solomon encoding of one default stripe.
func BenchmarkEncode(b *testing.B) {
	for _, sh := range benchShapes {
		b.Run(fmt.Sprintf("%d+%d", sh.k, sh.m), func(b *testing.B) {
			enc, err := reedsolomon.New(sh.k, sh.m, reedsolomon.WithAutoGoroutines(bitrot.ShardBlockSize(erasure.DefaultStripeSize, sh.k)))
			if err != nil {
				b.Fatal(err)
			}
			block := bitrot.ShardBlockSize(erasure.DefaultStripeSize, sh.k)
			shards := make([][]byte, sh.k+sh.m)
			for i := range shards {
				shards[i] = pseudo(block, uint64(i))
			}
			b.SetBytes(int64(sh.k * block))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := enc.Encode(shards); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDecode measures reconstruction of one lost data shard per stripe.
func BenchmarkDecode(b *testing.B) {
	for _, sh := range benchShapes {
		b.Run(fmt.Sprintf("%d+%d", sh.k, sh.m), func(b *testing.B) {
			enc, err := reedsolomon.New(sh.k, sh.m, reedsolomon.WithAutoGoroutines(bitrot.ShardBlockSize(erasure.DefaultStripeSize, sh.k)))
			if err != nil {
				b.Fatal(err)
			}
			block := bitrot.ShardBlockSize(erasure.DefaultStripeSize, sh.k)
			all := make([][]byte, sh.k+sh.m)
			for i := range all {
				all[i] = make([]byte, block)
				if i < sh.k {
					copy(all[i], pseudo(block, uint64(i)))
				}
			}
			if err := enc.Encode(all); err != nil {
				b.Fatal(err)
			}
			shards := make([][]byte, sh.k+sh.m)
			b.SetBytes(int64(sh.k * block))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for i := range shards {
					shards[i] = all[i]
				}
				shards[0] = all[0][:0]
				if err := enc.ReconstructData(shards); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkWrite measures a full object write, including the removal of the
// written shards between iterations, which is not timed.
func BenchmarkWrite(b *testing.B) {
	data := pseudo(benchObject, 1)
	for _, sh := range benchShapes {
		b.Run(fmt.Sprintf("%d+%d", sh.k, sh.m), func(b *testing.B) {
			s, _ := benchSet(b, sh.k, sh.m)
			ctx := context.Background()
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				id := ids.NewDataID()
				layout, err := s.Write(ctx, id, bytes.NewReader(data), int64(len(data)))
				if err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				if err := s.Delete(ctx, id, layout); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
		})
	}
}

// BenchmarkRead measures streaming a whole object. The degraded variant takes
// data shard 0 offline, so every stripe is reconstructed from parity.
func BenchmarkRead(b *testing.B) {
	data := pseudo(benchObject, 2)
	for _, sh := range benchShapes {
		for _, degraded := range []bool{false, true} {
			if degraded && sh.m == 0 {
				continue
			}
			name := fmt.Sprintf("%d+%d/healthy", sh.k, sh.m)
			if degraded {
				name = fmt.Sprintf("%d+%d/reconstruct", sh.k, sh.m)
			}
			b.Run(name, func(b *testing.B) {
				s, mem := benchSet(b, sh.k, sh.m)
				ctx := context.Background()
				id := ids.NewDataID()
				layout, err := s.Write(ctx, id, bytes.NewReader(data), int64(len(data)))
				if err != nil {
					b.Fatal(err)
				}
				if degraded {
					mem[0].SetFaults(erasuretest.Faults{Offline: true})
				}
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					rc, err := s.Read(ctx, id, layout, int64(len(data)), 0, int64(len(data)))
					if err != nil {
						b.Fatal(err)
					}
					if _, err := io.Copy(io.Discard, rc); err != nil {
						b.Fatal(err)
					}
					if err := rc.Close(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

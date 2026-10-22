package rs

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/erasure/erasuretest"
)

// FuzzRoundTrip writes an object under a random shape and stripe size,
// damages up to M of its shards, and requires a random range to read back
// exactly. Damage is either a dead drive or a flipped byte at a random offset.
func FuzzRoundTrip(f *testing.F) {
	f.Add(uint16(1000), uint8(2), uint8(1), uint32(5000), uint32(10), uint32(3000), uint8(0b101), uint64(1))
	f.Add(uint16(1), uint8(0), uint8(0), uint32(0), uint32(0), uint32(0), uint8(0), uint64(2))
	f.Add(uint16(4095), uint8(5), uint8(3), uint32(19999), uint32(7), uint32(19000), uint8(0b111), uint64(3))
	f.Fuzz(func(t *testing.T, stripe uint16, k8, m8 uint8, size32, off32, len32 uint32, faults uint8, seed uint64) {
		k := int(k8%6) + 1
		m := int(m8 % 4)
		s, mem := newTestSet(t, k, m, Options{StripeSize: int(stripe%4095) + 1})
		size := int64(size32 % 20000)
		data := pseudo(int(size), seed)
		id, layout := mustWrite(t, s, data)

		rng := rand.New(rand.NewPCG(seed, uint64(faults)))
		shardLen := bitrot.ShardSize(size, k, layout.StripeSize)
		for _, i := range rng.Perm(k + m)[:min(int(faults)%(m+1), m)] {
			if shardLen > 0 && rng.IntN(2) == 0 {
				if err := mem[i].Corrupt(shardID(id, i), rng.IntN(int(shardLen))); err != nil {
					t.Fatal(err)
				}
				continue
			}
			mem[i].SetFaults(erasuretest.Faults{Offline: true})
		}

		off, n := int64(0), size
		if size > 0 {
			off = int64(off32) % size
			n = int64(len32) % (size - off + 1)
		}
		if got := mustReadAll(t, s, id, layout, size, off, n); !bytes.Equal(got, data[off:off+n]) {
			t.Fatalf("range [%d,+%d) of %d bytes with %d+%d shape differs", off, n, size, k, m)
		}
	})
}

package rs

import (
	"bytes"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
)

// TestMisdirectedShardIsRebuiltAndHealed places shard 1 of another object on
// drive 0 in place of shard 0. Its bytes are valid, but at another position,
// so the read must rebuild the stripes from the other shards and queue shard
// 0 for repair. Heal must then restore the original bytes.
func TestMisdirectedShardIsRebuiltAndHealed(t *testing.T) {
	s, mem := newTestSet(t, 2, 1, Options{})
	dataA := pseudo(2*erasure.DefaultStripeSize+3, 31)
	idA, layoutA := mustWrite(t, s, dataA)
	idB, _ := mustWrite(t, s, pseudo(len(dataA), 32))

	original, ok := mem[0].Shard(shardID(idA, 0))
	if !ok {
		t.Fatal("shard 0 not stored")
	}
	foreign, ok := mem[1].Shard(shardID(idB, 1))
	if !ok {
		t.Fatal("foreign shard not stored")
	}
	mem[0].Replace(shardID(idA, 0), foreign)

	size := int64(len(dataA))
	if got := mustReadAll(t, s, idA, layoutA, size, 0, size); !bytes.Equal(got, dataA) {
		t.Fatal("read with a misdirected shard returned wrong bytes")
	}
	req, err := s.HealQueue().Pop(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if req.Shard != 0 {
		t.Fatalf("heal queue reports shard %d, want 0", req.Shard)
	}

	healed, err := s.Heal(testCtx(t), idA, layoutA, size)
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}
	if healed.Drives[0] != layoutA.Drives[0] {
		t.Fatal("Heal changed the drive of a shard it rewrote in place")
	}
	if got, _ := mem[0].Shard(shardID(idA, 0)); !bytes.Equal(got, original) {
		t.Fatal("healed shard differs from the original bytes")
	}
}

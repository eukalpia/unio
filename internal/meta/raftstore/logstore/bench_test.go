package logstore

import (
	"bytes"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/hashicorp/raft"
)

// BenchmarkStoreLogsBatch stores batches of 64 entries of 256 bytes into one
// group. Each call is one Pebble batch committed with an fsync, so ns/op
// mostly measures the disk's sync cost. It asserts nothing.
func BenchmarkStoreLogsBatch(b *testing.B) {
	const (
		batchSize = 64
		payload   = 256
	)
	cache := pebble.NewCache(16 << 20)
	b.Cleanup(cache.Unref)
	s, err := Open(b.TempDir(), cache)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() {
		if err := s.Close(); err != nil {
			b.Errorf("Close: %v", err)
		}
	})

	g := s.Group(1)
	data := bytes.Repeat([]byte{0xAB}, payload)
	batch := make([]*raft.Log, batchSize)
	for i := range batch {
		batch[i] = &raft.Log{Term: 1, Type: raft.LogCommand, Data: data}
	}
	next := uint64(1)

	b.SetBytes(batchSize * payload)
	b.ResetTimer()
	for range b.N {
		for _, l := range batch {
			l.Index = next
			next++
		}
		if err := g.StoreLogs(batch); err != nil {
			b.Fatalf("StoreLogs: %v", err)
		}
	}
}

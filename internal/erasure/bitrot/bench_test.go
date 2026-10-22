package bitrot

import (
	"bytes"
	"io"
	"testing"
)

// benchBlock is a typical block: a 1 MiB stripe split across 8 shards.
const benchBlock = 128 << 10

func BenchmarkDigest(b *testing.B) {
	data := make([]byte, benchBlock)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		_ = digest(testPos, 7, data)
	}
}

func BenchmarkWriteBlock(b *testing.B) {
	data := make([]byte, benchBlock)
	w := NewWriter(io.Discard, testPos)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if err := w.WriteBlock(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadBlock(b *testing.B) {
	data := make([]byte, benchBlock)
	var buf bytes.Buffer
	if err := NewWriter(&buf, testPos).WriteBlock(data); err != nil {
		b.Fatal(err)
	}
	stream := buf.Bytes()
	dst := make([]byte, benchBlock)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := NewReader(bytes.NewReader(stream), testPos)
		if err := r.ReadBlock(dst); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSpan(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Span(1<<40, 8, 1<<20, 3<<30+17, 64<<20); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBlockSeed measures the per-block cost the position binding adds
// on top of hashing the block data.
func BenchmarkBlockSeed(b *testing.B) {
	b.ReportAllocs()
	var sink uint64
	for b.Loop() {
		sink += blockSeed(testPos.Data, testPos.Shard, uint64(sink&7))
	}
	_ = sink
}

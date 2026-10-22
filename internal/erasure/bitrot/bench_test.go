package bitrot

import (
	"bytes"
	"io"
	"testing"
)

// benchBlock is a typical block: a 1 MiB stripe split across 8 shards.
const benchBlock = 128 << 10

func BenchmarkSum(b *testing.B) {
	data := make([]byte, benchBlock)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		_ = Sum(data)
	}
}

func BenchmarkWriteBlock(b *testing.B) {
	data := make([]byte, benchBlock)
	w := NewWriter(io.Discard)
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
	if err := NewWriter(&buf).WriteBlock(data); err != nil {
		b.Fatal(err)
	}
	stream := buf.Bytes()
	dst := make([]byte, benchBlock)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := NewReader(bytes.NewReader(stream))
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

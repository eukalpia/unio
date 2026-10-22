package stats

import (
	"testing"
	"time"
)

// benchSamples returns n deterministic, unsorted latencies.
func benchSamples(n int) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = time.Duration((i*7919)%n+1) * time.Microsecond
	}
	return out
}

func BenchmarkPercentile(b *testing.B) {
	samples := benchSamples(10_000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Percentile(samples, 99)
	}
}

func BenchmarkSummarize(b *testing.B) {
	samples := benchSamples(100_000)
	c := Collected{Samples: samples, Operations: int64(len(samples)), Bytes: 4096 * int64(len(samples))}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Summarize(c, time.Second)
	}
}

func BenchmarkRecorderSuccess(b *testing.B) {
	// The limit is smaller than b.N on purpose: the benchmark then covers both
	// the append path and the dropped-counter path.
	r := NewRecorder(1 << 20)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.Success(time.Millisecond, 4096)
	}
}

package stats

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"
	"time"
)

// FuzzPercentile checks that Percentile never panics and that, for any
// non-empty input, the result is one of the input values and lies between
// the minimum and the maximum. The input bytes are decoded as little-endian
// int64 nanosecond values; a trailing partial value is ignored.
func FuzzPercentile(f *testing.F) {
	f.Add([]byte{}, 50.0)
	f.Add(encodeNanos(1, 2, 3), 0.0)
	f.Add(encodeNanos(5, 5, 5, 1, 1), 40.0)
	f.Add(encodeNanos(-7, 0, 9), 100.0)
	f.Add(encodeNanos(math.MaxInt64, math.MinInt64), 99.9)
	f.Add(encodeNanos(3), math.NaN())
	f.Add(encodeNanos(1, 2), math.Inf(1))

	f.Fuzz(func(t *testing.T, raw []byte, p float64) {
		samples := decodeNanos(raw)
		before := slices.Clone(samples)

		got := Percentile(samples, p)

		if !slices.Equal(samples, before) {
			t.Fatalf("Percentile modified its input")
		}
		if len(samples) == 0 {
			if got != 0 {
				t.Fatalf("Percentile of empty input = %v, want 0", got)
			}
			return
		}
		lo, hi := samples[0], samples[0]
		for _, s := range samples {
			lo = min(lo, s)
			hi = max(hi, s)
		}
		if got < lo || got > hi {
			t.Fatalf("Percentile(p=%v) = %v outside [%v, %v]", p, got, lo, hi)
		}
		if !slices.Contains(samples, got) {
			t.Fatalf("Percentile(p=%v) = %v is not one of the input values", p, got)
		}
	})
}

func encodeNanos(values ...int64) []byte {
	out := make([]byte, 0, 8*len(values))
	for _, v := range values {
		out = binary.LittleEndian.AppendUint64(out, uint64(v))
	}
	return out
}

func decodeNanos(raw []byte) []time.Duration {
	out := make([]time.Duration, 0, len(raw)/8)
	for len(raw) >= 8 {
		out = append(out, time.Duration(int64(binary.LittleEndian.Uint64(raw))))
		raw = raw[8:]
	}
	return out
}

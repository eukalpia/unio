package stats

import (
	"math"
	"slices"
	"testing"
	"time"
)

func ms(values ...int) []time.Duration {
	out := make([]time.Duration, len(values))
	for i, v := range values {
		out[i] = time.Duration(v) * time.Millisecond
	}
	return out
}

func TestPercentile(t *testing.T) {
	seq20 := make([]int, 20)
	for i := range seq20 {
		seq20[i] = i + 1
	}
	tests := []struct {
		name    string
		samples []time.Duration
		p       float64
		want    time.Duration
	}{
		{name: "empty p50", samples: nil, p: 50, want: 0},
		{name: "empty p100", samples: []time.Duration{}, p: 100, want: 0},
		{name: "single p0", samples: ms(7), p: 0, want: 7 * time.Millisecond},
		{name: "single p50", samples: ms(7), p: 50, want: 7 * time.Millisecond},
		{name: "single p100", samples: ms(7), p: 100, want: 7 * time.Millisecond},
		{name: "sorted p10 rank 1", samples: ms(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), p: 10, want: 1 * time.Millisecond},
		{name: "sorted p50 rank 5", samples: ms(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), p: 50, want: 5 * time.Millisecond},
		{name: "sorted p95 rank 10", samples: ms(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), p: 95, want: 10 * time.Millisecond},
		{name: "sorted p99 rank 10", samples: ms(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), p: 99, want: 10 * time.Millisecond},
		{name: "sorted p0 clamps to min", samples: ms(1, 2, 3), p: 0, want: 1 * time.Millisecond},
		{name: "unsorted p50", samples: ms(9, 2, 10, 4, 1, 6, 8, 3, 5, 7), p: 50, want: 5 * time.Millisecond},
		{name: "unsorted p99", samples: ms(9, 2, 10, 4, 1, 6, 8, 3, 5, 7), p: 99, want: 10 * time.Millisecond},
		{name: "duplicates p40 rank 2", samples: ms(5, 5, 5, 1, 1), p: 40, want: 1 * time.Millisecond},
		{name: "duplicates p50 rank 3", samples: ms(5, 5, 5, 1, 1), p: 50, want: 5 * time.Millisecond},
		{name: "duplicates all equal", samples: ms(4, 4, 4, 4), p: 99, want: 4 * time.Millisecond},
		{name: "p100 is max", samples: ms(3, 1, 2), p: 100, want: 3 * time.Millisecond},
		{name: "p above 100 clamps to max", samples: ms(3, 1, 2), p: 150, want: 3 * time.Millisecond},
		{name: "negative p clamps to min", samples: ms(3, 1, 2), p: -5, want: 1 * time.Millisecond},
		{name: "NaN p is treated as 0", samples: ms(3, 1, 2), p: math.NaN(), want: 1 * time.Millisecond},
		{name: "p95 of 20 is rank 19 despite float error", samples: ms(seq20...), p: 95, want: 19 * time.Millisecond},
		{name: "p99.9 of 1000 is rank 999", samples: seqDurations(1000), p: 99.9, want: 999 * time.Millisecond},
		{name: "half rank rounds up", samples: ms(1, 2, 3, 4), p: 50, want: 2 * time.Millisecond},
		{name: "just above half rank rounds up", samples: ms(1, 2, 3, 4), p: 50.01, want: 3 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Percentile(tc.samples, tc.p)
			if got != tc.want {
				t.Fatalf("Percentile(p=%v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

func seqDurations(n int) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = time.Duration(i+1) * time.Millisecond
	}
	return out
}

func TestPercentileDoesNotMutateInput(t *testing.T) {
	in := ms(9, 2, 10, 4, 1)
	before := slices.Clone(in)
	_ = Percentile(in, 50)
	if !slices.Equal(in, before) {
		t.Fatalf("input was modified: got %v, want %v", in, before)
	}
}

func TestNearestRank(t *testing.T) {
	tests := []struct {
		p    float64
		n    int
		want int
	}{
		{p: 0, n: 5, want: 1},
		{p: 50, n: 4, want: 2},
		{p: 50, n: 3, want: 2},
		{p: 100, n: 5, want: 5},
		{p: 95, n: 20, want: 19},
		{p: 99.9, n: 1000, want: 999},
		{p: 100, n: 8_000_000, want: 8_000_000},
		{p: 99, n: 8_000_000, want: 7_920_000},
	}
	for _, tc := range tests {
		if got := nearestRank(tc.p, tc.n); got != tc.want {
			t.Errorf("nearestRank(%v, %d) = %d, want %d", tc.p, tc.n, got, tc.want)
		}
	}
}

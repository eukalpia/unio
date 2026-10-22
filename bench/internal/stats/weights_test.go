package stats

import (
	"cmp"
	"math"
	"slices"
	"testing"
	"time"
)

// TestWeightedPercentile checks the weighted nearest-rank rule on a small case.
// The fast sample stands for one observation and the slow one for three, so the
// median is the slow sample.
func TestWeightedPercentile(t *testing.T) {
	t.Parallel()
	items := []weightedSample{{d: time.Millisecond, w: 1}, {d: 100 * time.Millisecond, w: 3}}
	total := 4.0
	tests := []struct {
		p    float64
		want time.Duration
	}{
		{p: 0, want: time.Millisecond},
		{p: 25, want: time.Millisecond},
		{p: 26, want: 100 * time.Millisecond},
		{p: 50, want: 100 * time.Millisecond},
		{p: 100, want: 100 * time.Millisecond},
	}
	for _, tc := range tests {
		if got := weightedPercentile(items, tc.p, total); got != tc.want {
			t.Errorf("p%v = %v, want %v", tc.p, got, tc.want)
		}
	}
}

// TestWeightedPercentileMatchesNearestRankForUniformWeights checks that unit
// weights reproduce the nearest-rank percentile exactly, so the golden summaries
// for unweighted samples do not change.
func TestWeightedPercentileMatchesNearestRankForUniformWeights(t *testing.T) {
	t.Parallel()
	for n := 1; n <= 60; n++ {
		items := make([]weightedSample, n)
		durations := make([]time.Duration, n)
		for i := range items {
			d := time.Duration(i*i+1) * time.Microsecond
			items[i] = weightedSample{d: d, w: 1}
			durations[i] = d
		}
		slices.SortFunc(items, func(a, b weightedSample) int { return cmp.Compare(a.d, b.d) })
		slices.Sort(durations)
		for _, p := range []float64{0, 1, 10, 25, 33.3, 50, 90, 95, 99, 100} {
			want := percentileSorted(durations, p)
			if got := weightedPercentile(items, p, float64(n)); got != want {
				t.Fatalf("n=%d p=%v: weighted %v, nearest rank %v", n, p, got, want)
			}
		}
	}
}

// TestMergeWeightsByObservationCount checks that a worker which completed more
// operations contributes proportionally more to the mean. Unweighted, the mean
// of the kept samples is 50.5 ms; weighted by observations it is 10 ms, because
// the fast worker completed ten times as many operations.
func TestMergeWeightsByObservationCount(t *testing.T) {
	t.Parallel()
	fast := NewRecorder(50, testRNG(1))
	slow := NewRecorder(50, testRNG(2))
	for range 1000 {
		fast.Success(time.Millisecond, 1)
	}
	for range 100 {
		slow.Success(100*time.Millisecond, 1)
	}
	c := Merge(fast, slow)
	if got := Summarize(c, time.Second).LatencyMs.Mean; math.Abs(got-10) > 1e-9 {
		t.Fatalf("weighted mean = %v ms, want 10", got)
	}
}

// TestMergeWeightsSumToObservations checks that the weights of a merged
// collection add up to the number of successful operations, so no observation
// is counted twice or lost from the represented population.
func TestMergeWeightsSumToObservations(t *testing.T) {
	t.Parallel()
	a := NewRecorder(3, testRNG(1))
	b := NewRecorder(10, testRNG(2))
	for i := range 20 {
		a.Success(time.Duration(i+1)*time.Microsecond, 0)
	}
	for i := range 5 {
		b.Success(time.Duration(i+1)*time.Microsecond, 0)
	}
	c := Merge(a, b)
	if len(c.Weights) != len(c.Samples) {
		t.Fatalf("%d weights for %d samples", len(c.Weights), len(c.Samples))
	}
	var total float64
	for _, w := range c.Weights {
		total += w
	}
	if math.Abs(total-float64(c.Operations)) > 1e-9 {
		t.Fatalf("weights sum to %v, want %d operations", total, c.Operations)
	}
	if c.Dropped != 25-int64(len(c.Samples)) {
		t.Fatalf("dropped = %d, want %d", c.Dropped, 25-int64(len(c.Samples)))
	}
}

// TestCombineKeepsWeightsAndTreatsMissingWeightsAsOne checks that combining
// collections keeps each sample's weight, and that a collection without weights
// counts each sample once.
func TestCombineKeepsWeightsAndTreatsMissingWeightsAsOne(t *testing.T) {
	t.Parallel()
	weighted := Collected{Samples: ms(1, 2), Weights: []float64{4, 4}, Operations: 8}
	plain := Collected{Samples: ms(3), Operations: 1}
	got := Combine(weighted, plain)
	if want := []float64{4, 4, 1}; !slices.Equal(got.Weights, want) {
		t.Fatalf("weights = %v, want %v", got.Weights, want)
	}
	if got.Operations != 9 {
		t.Fatalf("operations = %d, want 9", got.Operations)
	}
}

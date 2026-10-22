// Package stats computes latency percentiles, throughput and summary
// statistics for benchmark runs. It depends only on the standard library.
package stats

import (
	"math"
	"slices"
	"time"
)

// relTolerance absorbs floating-point error in p/100*n so that exact ranks,
// such as p=95 with n=20, are not pushed up by one.
const relTolerance = 1e-12

// Percentile returns the p-th percentile of samples using the nearest-rank
// method. For n samples the rank is ceil(p/100 * n), 1-based, so the result is
// always one of the input values. p is clamped to [0, 100]; NaN is treated as
// 0. An empty input yields 0.
//
// Percentile sorts a private copy of samples; the caller's slice is not
// modified.
func Percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	return percentileSorted(sorted, p)
}

// percentileSorted returns the nearest-rank percentile of an ascending,
// non-empty slice.
func percentileSorted(sorted []time.Duration, p float64) time.Duration {
	return sorted[nearestRank(p, len(sorted))-1]
}

// nearestRank returns the 1-based nearest-rank index for p among n samples.
// n must be at least 1.
func nearestRank(p float64, n int) int {
	if math.IsNaN(p) || p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	x := p / 100 * float64(n)
	rank := int(math.Ceil(x - x*relTolerance))
	return min(max(rank, 1), n)
}

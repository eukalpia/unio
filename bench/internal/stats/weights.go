package stats

import (
	"math"
	"time"
)

// weightedSample is one kept latency and the number of observations it
// represents.
type weightedSample struct {
	d time.Duration
	w float64
}

// weightAt returns the weight of sample i of c. Without usable weights every
// sample counts once.
func weightAt(c Collected, i int) float64 {
	if len(c.Weights) != len(c.Samples) {
		return 1
	}
	return c.Weights[i]
}

// weightedPercentile returns the p-th percentile of items, which must be sorted
// by latency. Each item counts w observations, and total is the sum of the
// weights. It is the weighted form of the nearest-rank method: the first item
// whose cumulative weight reaches p/100 of the total. With unit weights it
// returns the same value as percentileSorted. p is clamped to [0, 100].
func weightedPercentile(items []weightedSample, p float64, total float64) time.Duration {
	if math.IsNaN(p) || p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	threshold := p / 100 * total
	threshold -= threshold * relTolerance
	var cum float64
	for _, it := range items {
		cum += it.w
		if cum >= threshold {
			return it.d
		}
	}
	return items[len(items)-1].d
}

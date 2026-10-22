package stats

import (
	"cmp"
	"slices"
	"time"
)

// mib is one mebibyte in bytes.
const mib = 1 << 20

// Summary is the result of one operation type. Its JSON field names are part
// of the report schema and must not change without a schema version bump.
//
// Operations counts successful operations. The percentiles and the mean are
// estimates of the whole window from weighted samples, so they describe every
// operation, not only the kept ones. Min and Max are the extremes of the kept
// samples. DroppedSamples counts successful operations that no sample
// represents.
type Summary struct {
	Operations      int64   `json:"operations"`
	Errors          int64   `json:"errors"`
	DroppedSamples  int64   `json:"dropped_samples"`
	Bytes           int64   `json:"bytes"`
	DurationSeconds float64 `json:"duration_seconds"`
	OpsPerSecond    float64 `json:"ops_per_second"`
	MiBPerSecond    float64 `json:"mib_per_second"`
	LatencyMs       Latency `json:"latency_ms"`
}

// Latency holds latency statistics in milliseconds.
type Latency struct {
	Min  float64 `json:"min"`
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Max  float64 `json:"max"`
}

// Summarize computes the summary of one operation type over the measurement
// window elapsed. A zero or negative window yields zero rates, never a
// division by zero. Empty input yields zero latency statistics.
func Summarize(c Collected, elapsed time.Duration) Summary {
	return Summary{
		Operations:      c.Operations,
		Errors:          c.Errors,
		DroppedSamples:  c.Dropped,
		Bytes:           c.Bytes,
		DurationSeconds: seconds(elapsed),
		OpsPerSecond:    perSecond(float64(c.Operations), elapsed),
		MiBPerSecond:    perSecond(float64(c.Bytes)/mib, elapsed),
		LatencyMs:       latency(c),
	}
}

// latency computes the statistics of a collection from one sorted copy of its
// samples. The mean and the percentiles are weighted by the observations each
// sample represents. Min and max are the extremes of the samples.
func latency(c Collected) Latency {
	n := len(c.Samples)
	if n == 0 {
		return Latency{}
	}
	items := make([]weightedSample, n)
	var totalWeight, weightedSum float64
	for i, d := range c.Samples {
		w := weightAt(c, i)
		items[i] = weightedSample{d: d, w: w}
		totalWeight += w
		weightedSum += float64(d) * w
	}
	slices.SortFunc(items, func(a, b weightedSample) int { return cmp.Compare(a.d, b.d) })

	var mean float64
	if totalWeight > 0 {
		mean = weightedSum / totalWeight
	}
	return Latency{
		Min:  toMs(float64(items[0].d)),
		Mean: toMs(mean),
		P50:  toMs(float64(weightedPercentile(items, 50, totalWeight))),
		P95:  toMs(float64(weightedPercentile(items, 95, totalWeight))),
		P99:  toMs(float64(weightedPercentile(items, 99, totalWeight))),
		Max:  toMs(float64(items[n-1].d)),
	}
}

// toMs converts nanoseconds to milliseconds.
func toMs(nanos float64) float64 {
	return nanos / float64(time.Millisecond)
}

// seconds returns d in seconds, or 0 for a non-positive duration.
func seconds(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return d.Seconds()
}

// perSecond divides amount by elapsed in seconds, or returns 0 when the window
// is empty.
func perSecond(amount float64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return amount / elapsed.Seconds()
}

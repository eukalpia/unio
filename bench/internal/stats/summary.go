package stats

import (
	"slices"
	"time"
)

// mib is one mebibyte in bytes.
const mib = 1 << 20

// Summary is the result of one operation type. Its JSON field names are part
// of the report schema and must not change without a schema version bump.
//
// Operations counts successful operations. Latency statistics cover the kept
// samples only; DroppedSamples counts successful operations whose latency was
// discarded because the recorder reached its limit.
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
		LatencyMs:       latency(c.Samples),
	}
}

// latency computes min, mean, percentiles and max from one sorted copy of the
// samples.
func latency(samples []time.Duration) Latency {
	if len(samples) == 0 {
		return Latency{}
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)

	var sum float64
	for _, d := range sorted {
		sum += float64(d)
	}
	return Latency{
		Min:  toMs(float64(sorted[0])),
		Mean: toMs(sum / float64(len(sorted))),
		P50:  toMs(float64(percentileSorted(sorted, 50))),
		P95:  toMs(float64(percentileSorted(sorted, 95))),
		P99:  toMs(float64(percentileSorted(sorted, 99))),
		Max:  toMs(float64(sorted[len(sorted)-1])),
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

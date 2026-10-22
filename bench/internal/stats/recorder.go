package stats

import "time"

// MaxTotalSamples bounds the latency samples kept by one run across all
// workers and operation types. At 8 Mi samples of 8 bytes each, sample memory
// is capped at 64 MiB regardless of run length.
const MaxTotalSamples = 8 << 20

// SampleLimit returns how many samples each of recorders recorders may keep so
// that together they stay within MaxTotalSamples. It returns at least 1.
func SampleLimit(recorders int) int {
	return max(MaxTotalSamples/max(recorders, 1), 1)
}

// Recorder accumulates the results of one operation type in one worker.
//
// Concurrency model: a Recorder is owned by exactly one goroutine while the
// run is in progress. Workers never share a Recorder, so recording takes no
// locks. After the workers have stopped, the recorders are passed to Merge.
// Each recorder keeps at most its limit of samples; operations beyond that
// still update the counters and are reported as dropped.
type Recorder struct {
	limit   int
	samples []time.Duration
	ops     int64
	errors  int64
	bytes   int64
	dropped int64
}

// NewRecorder returns a Recorder that keeps at most limit latency samples. A
// negative limit is treated as zero.
func NewRecorder(limit int) *Recorder {
	return &Recorder{limit: max(limit, 0)}
}

// Success records one successful operation with its latency and payload size
// in bytes.
func (r *Recorder) Success(latency time.Duration, bytes int64) {
	r.ops++
	r.bytes += bytes
	if len(r.samples) < r.limit {
		r.samples = append(r.samples, latency)
		return
	}
	r.dropped++
}

// Failure records one failed operation. A failure has no latency sample.
func (r *Recorder) Failure() {
	r.errors++
}

// Collected is the merged result of one operation type.
type Collected struct {
	// Samples holds the latencies of the kept successful operations.
	Samples []time.Duration
	// Operations counts all successful operations, including Dropped.
	Operations int64
	// Errors counts failed operations.
	Errors int64
	// Bytes is the total payload size of successful operations.
	Bytes int64
	// Dropped counts successful operations whose latency was not kept.
	Dropped int64
}

// Merge combines recorders into one Collected value. Nil recorders are
// skipped. Merge must be called only after the owning goroutines have stopped.
func Merge(recs ...*Recorder) Collected {
	total := 0
	for _, r := range recs {
		if r != nil {
			total += len(r.samples)
		}
	}
	c := Collected{Samples: make([]time.Duration, 0, total)}
	for _, r := range recs {
		if r == nil {
			continue
		}
		c.Samples = append(c.Samples, r.samples...)
		c.Operations += r.ops
		c.Errors += r.errors
		c.Bytes += r.bytes
		c.Dropped += r.dropped
	}
	return c
}

// Combine adds several Collected values, for example the per-operation results
// of one workload, into a single total.
func Combine(parts ...Collected) Collected {
	total := 0
	for _, p := range parts {
		total += len(p.Samples)
	}
	c := Collected{Samples: make([]time.Duration, 0, total)}
	for _, p := range parts {
		c.Samples = append(c.Samples, p.Samples...)
		c.Operations += p.Operations
		c.Errors += p.Errors
		c.Bytes += p.Bytes
		c.Dropped += p.Dropped
	}
	return c
}

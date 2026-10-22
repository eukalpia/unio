package stats

import (
	"math/rand/v2"
	"time"
)

// MaxTotalSamples bounds the latency samples kept by one run across all
// workers and operation types. Each sample carries a duration and a weight, 8
// bytes each, so sample memory is capped at 128 MiB regardless of run length.
const MaxTotalSamples = 8 << 20

// SampleLimit returns how many samples each of recorders recorders may keep so
// that together they stay within MaxTotalSamples. It returns at least 1.
func SampleLimit(recorders int) int {
	return max(MaxTotalSamples/max(recorders, 1), 1)
}

// Recorder accumulates the results of one operation type in one worker.
//
// Latencies are kept by reservoir sampling (Algorithm R). The first limit
// latencies are kept. Each later one replaces a uniformly chosen kept latency
// with probability limit/n, where n is its position in the window. Every
// operation of the window therefore has the same chance of being represented,
// so percentiles describe the whole run and not only its start.
//
// Concurrency model: a Recorder and the generator it draws from are owned by
// exactly one goroutine while the run is in progress. Workers never share a
// Recorder, so recording takes no locks. After the workers have stopped, the
// recorders are passed to Merge. Operations beyond the kept samples still update
// the counters, and Collected.Dropped reports how many were not represented.
type Recorder struct {
	limit   int
	rng     *rand.Rand
	samples []time.Duration
	ops     int64
	errors  int64
	bytes   int64
}

// NewRecorder returns a Recorder that keeps at most limit latency samples. A
// negative limit is treated as zero. rng chooses which latencies are kept. It
// must not be used by another goroutine while the Recorder is in use, and it
// must be seeded per worker so that a run can be repeated. A nil rng uses a
// fixed seed.
func NewRecorder(limit int, rng *rand.Rand) *Recorder {
	if rng == nil {
		// #nosec G404 -- chooses which latency samples to keep; not security sensitive
		rng = rand.New(rand.NewPCG(1, 1))
	}
	return &Recorder{limit: max(limit, 0), rng: rng}
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
	// Algorithm R: the n-th observation is kept, in the slot of a uniformly
	// chosen sample, with probability limit/n. ops is at least 1 here.
	if j := r.rng.Int64N(r.ops); j < int64(r.limit) {
		r.samples[j] = latency
	}
}

// Failure records one failed operation. A failure has no latency sample.
func (r *Recorder) Failure() {
	r.errors++
}

// Collected is the merged result of one operation type.
type Collected struct {
	// Samples holds the kept latencies of successful operations.
	Samples []time.Duration
	// Weights[i] is the number of observations that Samples[i] represents. A
	// nil or mismatched Weights means every sample represents one observation.
	Weights []float64
	// Operations counts all successful operations, including Dropped.
	Operations int64
	// Errors counts failed operations.
	Errors int64
	// Bytes is the total payload size of successful operations.
	Bytes int64
	// Dropped counts successful operations that no sample represents.
	Dropped int64
}

// Merge combines recorders into one Collected value. Nil recorders are
// skipped. Each sample of a recorder is weighted by the number of operations
// that recorder observed per kept sample, so a worker that completed more
// operations counts for more. Merge must be called only after the owning
// goroutines have stopped.
func Merge(recs ...*Recorder) Collected {
	total := 0
	for _, r := range recs {
		if r != nil {
			total += len(r.samples)
		}
	}
	c := Collected{
		Samples: make([]time.Duration, 0, total),
		Weights: make([]float64, 0, total),
	}
	for _, r := range recs {
		if r == nil {
			continue
		}
		c.Operations += r.ops
		c.Errors += r.errors
		c.Bytes += r.bytes
		c.Dropped += r.ops - int64(len(r.samples))
		if n := len(r.samples); n > 0 {
			w := float64(r.ops) / float64(n)
			c.Samples = append(c.Samples, r.samples...)
			for range n {
				c.Weights = append(c.Weights, w)
			}
		}
	}
	return c
}

// Combine adds several Collected values, for example the per-operation results
// of one workload, into a single total. Each sample keeps its weight.
func Combine(parts ...Collected) Collected {
	total := 0
	for _, p := range parts {
		total += len(p.Samples)
	}
	c := Collected{
		Samples: make([]time.Duration, 0, total),
		Weights: make([]float64, 0, total),
	}
	for _, p := range parts {
		for i, d := range p.Samples {
			c.Samples = append(c.Samples, d)
			c.Weights = append(c.Weights, weightAt(p, i))
		}
		c.Operations += p.Operations
		c.Errors += p.Errors
		c.Bytes += p.Bytes
		c.Dropped += p.Dropped
	}
	return c
}

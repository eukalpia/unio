package stats

import (
	"math/rand/v2"
	"testing"
	"time"
)

// testRNG returns a seeded generator, so that sampling in tests is repeatable.
func testRNG(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

// slowShare returns the fraction of the represented observations in c whose
// latency is at least threshold.
func slowShare(c Collected, threshold time.Duration) float64 {
	var slow, total float64
	for i, d := range c.Samples {
		w := weightAt(c, i)
		total += w
		if d >= threshold {
			slow += w
		}
	}
	if total == 0 {
		return 0
	}
	return slow / total
}

// TestReservoirReflectsWholeWindow is the failure the review found. The first
// recorder implementation kept only the first samples, so a run whose latency
// changes halfway reported percentiles of the first half only. The kept samples
// must describe both halves.
func TestReservoirReflectsWholeWindow(t *testing.T) {
	t.Parallel()
	const ops, limit = 2000, 200
	r := NewRecorder(limit, testRNG(7))
	for i := range ops {
		latency := time.Millisecond
		if i >= ops/2 {
			latency = 100 * time.Millisecond
		}
		r.Success(latency, 1)
	}
	c := Merge(r)

	if share := slowShare(c, 100*time.Millisecond); share < 0.35 || share > 0.65 {
		t.Fatalf("slow half represented by %.2f of the samples, want about 0.5", share)
	}
	s := Summarize(c, time.Second)
	if s.LatencyMs.P95 != 100 {
		t.Fatalf("p95 = %v ms, want 100: the percentile ignores the second half", s.LatencyMs.P95)
	}
	if s.LatencyMs.Mean < 45 || s.LatencyMs.Mean > 56 {
		t.Fatalf("mean = %v ms, want about 50.5", s.LatencyMs.Mean)
	}
}

// TestReservoirReflectsWholeWindowAcrossWorkers checks the same property for a
// merged run: eight workers, each with the same latency change halfway.
func TestReservoirReflectsWholeWindowAcrossWorkers(t *testing.T) {
	t.Parallel()
	const workers, ops, limit = 8, 1000, 100
	recs := make([]*Recorder, workers)
	for w := range recs {
		recs[w] = NewRecorder(limit, testRNG(uint64(w)+1))
		for i := range ops {
			latency := time.Millisecond
			if i >= ops/2 {
				latency = 100 * time.Millisecond
			}
			recs[w].Success(latency, 1)
		}
	}
	s := Summarize(Merge(recs...), time.Second)
	if s.LatencyMs.P95 != 100 {
		t.Fatalf("p95 = %v ms, want 100", s.LatencyMs.P95)
	}
	if s.DroppedSamples != workers*ops-workers*limit {
		t.Fatalf("dropped = %d, want %d", s.DroppedSamples, workers*ops-workers*limit)
	}
}

// TestReservoirIsUniformOverTheWindow checks that samples are spread across the
// whole window. Latencies rise linearly, so a uniform sample has a mean near the
// middle and only about one percent of its values in the first percent of time.
func TestReservoirIsUniformOverTheWindow(t *testing.T) {
	t.Parallel()
	const ops, limit = 10000, 1000
	r := NewRecorder(limit, testRNG(3))
	for i := range ops {
		r.Success(time.Duration(i+1)*time.Microsecond, 0)
	}
	c := Merge(r)
	if len(c.Samples) != limit {
		t.Fatalf("kept %d samples, want %d", len(c.Samples), limit)
	}
	var sum float64
	early := 0
	for _, d := range c.Samples {
		sum += float64(d / time.Microsecond)
		if d <= 100*time.Microsecond {
			early++
		}
	}
	if mean := sum / limit; mean < 4750 || mean > 5250 {
		t.Fatalf("sample mean = %.1f µs, want about 5000 for a uniform sample", mean)
	}
	if early > 30 {
		t.Fatalf("%d samples come from the first 1%% of the window; a uniform sample has about 10", early)
	}
}

// TestReservoirIsDeterministicForASeed checks that a fixed seed reproduces the
// same kept samples, so that a run can be repeated exactly.
func TestReservoirIsDeterministicForASeed(t *testing.T) {
	t.Parallel()
	run := func() Collected {
		r := NewRecorder(50, testRNG(11))
		for i := range 500 {
			r.Success(time.Duration(i+1)*time.Microsecond, 1)
		}
		return Merge(r)
	}
	a, b := run(), run()
	if len(a.Samples) != len(b.Samples) {
		t.Fatalf("lengths differ: %d and %d", len(a.Samples), len(b.Samples))
	}
	for i := range a.Samples {
		if a.Samples[i] != b.Samples[i] {
			t.Fatalf("sample %d differs: %v and %v", i, a.Samples[i], b.Samples[i])
		}
	}
}

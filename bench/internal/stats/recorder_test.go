package stats

import (
	"slices"
	"sync"
	"testing"
	"time"
)

func TestRecorderCountsSuccessesAndFailures(t *testing.T) {
	r := NewRecorder(10, testRNG(1))
	r.Success(1*time.Millisecond, 100)
	r.Success(3*time.Millisecond, 200)
	r.Failure()
	r.Failure()

	c := Merge(r)
	if want := ms(1, 3); !slices.Equal(c.Samples, want) {
		t.Errorf("samples = %v, want %v", c.Samples, want)
	}
	if c.Operations != 2 || c.Errors != 2 || c.Bytes != 300 || c.Dropped != 0 {
		t.Errorf("counters = ops %d errors %d bytes %d dropped %d, want 2 2 300 0",
			c.Operations, c.Errors, c.Bytes, c.Dropped)
	}
}

func TestRecorderLimitDropsSamplesButCountsOperations(t *testing.T) {
	tests := []struct {
		name        string
		limit       int
		successes   int
		wantSamples int
		wantDropped int64
	}{
		{name: "under limit", limit: 5, successes: 3, wantSamples: 3, wantDropped: 0},
		{name: "at limit", limit: 3, successes: 3, wantSamples: 3, wantDropped: 0},
		{name: "over limit", limit: 2, successes: 5, wantSamples: 2, wantDropped: 3},
		{name: "zero limit keeps no samples", limit: 0, successes: 4, wantSamples: 0, wantDropped: 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRecorder(tc.limit, testRNG(1))
			for i := 0; i < tc.successes; i++ {
				r.Success(time.Millisecond, 10)
			}
			c := Merge(r)
			if len(c.Samples) != tc.wantSamples {
				t.Errorf("kept %d samples, want %d", len(c.Samples), tc.wantSamples)
			}
			if c.Dropped != tc.wantDropped {
				t.Errorf("dropped = %d, want %d", c.Dropped, tc.wantDropped)
			}
			if c.Operations != int64(tc.successes) {
				t.Errorf("operations = %d, want %d", c.Operations, tc.successes)
			}
			if c.Bytes != int64(10*tc.successes) {
				t.Errorf("bytes = %d, want %d", c.Bytes, 10*tc.successes)
			}
		})
	}
}

func TestNewRecorderNegativeLimitIsZero(t *testing.T) {
	r := NewRecorder(-4, testRNG(1))
	r.Success(time.Millisecond, 1)
	if c := Merge(r); len(c.Samples) != 0 || c.Dropped != 1 {
		t.Fatalf("negative limit: samples %d dropped %d, want 0 and 1", len(c.Samples), c.Dropped)
	}
}

func TestMergeConcatenatesAndSumsCounters(t *testing.T) {
	a := NewRecorder(10, testRNG(1))
	a.Success(1*time.Millisecond, 5)
	a.Failure()
	b := NewRecorder(10, testRNG(1))
	b.Success(2*time.Millisecond, 7)
	b.Success(3*time.Millisecond, 9)

	c := Merge(a, nil, b)
	if want := ms(1, 2, 3); !slices.Equal(c.Samples, want) {
		t.Errorf("samples = %v, want %v", c.Samples, want)
	}
	if c.Operations != 3 || c.Errors != 1 || c.Bytes != 21 {
		t.Errorf("merged counters = ops %d errors %d bytes %d, want 3 1 21", c.Operations, c.Errors, c.Bytes)
	}
}

func TestMergeNoRecorders(t *testing.T) {
	c := Merge()
	if len(c.Samples) != 0 || c.Operations != 0 || c.Errors != 0 {
		t.Fatalf("Merge() = %+v, want zero value", c)
	}
}

func TestCombine(t *testing.T) {
	x := Collected{Samples: ms(1), Operations: 1, Errors: 1, Bytes: 2, Dropped: 0}
	y := Collected{Samples: ms(2, 3), Operations: 3, Errors: 0, Bytes: 4, Dropped: 1}
	got := Combine(x, y)
	if want := ms(1, 2, 3); !slices.Equal(got.Samples, want) {
		t.Errorf("samples = %v, want %v", got.Samples, want)
	}
	if got.Operations != 4 || got.Errors != 1 || got.Bytes != 6 || got.Dropped != 1 {
		t.Errorf("combined = %+v", got)
	}
}

func TestSampleLimit(t *testing.T) {
	tests := []struct {
		recorders int
		want      int
	}{
		{recorders: 0, want: MaxTotalSamples},
		{recorders: 1, want: MaxTotalSamples},
		{recorders: 512, want: MaxTotalSamples / 512},
		{recorders: MaxTotalSamples * 2, want: 1},
	}
	for _, tc := range tests {
		if got := SampleLimit(tc.recorders); got != tc.want {
			t.Errorf("SampleLimit(%d) = %d, want %d", tc.recorders, got, tc.want)
		}
	}
}

// TestRecordersAcrossGoroutines gives each goroutine its own Recorder, which
// is the sharing model documented on Recorder. Run with -race.
func TestRecordersAcrossGoroutines(t *testing.T) {
	const workers, perWorker = 64, 1000
	recs := make([]*Recorder, workers)
	for i := range recs {
		recs[i] = NewRecorder(SampleLimit(workers), testRNG(uint64(i)+1))
	}
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		go func(r *Recorder) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				r.Success(time.Duration(j+1)*time.Microsecond, 1)
			}
			r.Failure()
		}(recs[i])
	}
	wg.Wait()

	c := Merge(recs...)
	if c.Operations != workers*perWorker || c.Errors != workers || c.Bytes != workers*perWorker {
		t.Fatalf("merged = ops %d errors %d bytes %d, want %d %d %d",
			c.Operations, c.Errors, c.Bytes, workers*perWorker, workers, workers*perWorker)
	}
	if len(c.Samples) != workers*perWorker || c.Dropped != 0 {
		t.Fatalf("samples %d dropped %d, want %d and 0", len(c.Samples), c.Dropped, workers*perWorker)
	}
}

package stats

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	tests := []struct {
		name    string
		in      Collected
		elapsed time.Duration
		want    Summary
	}{
		{
			name:    "basic totals and rates",
			in:      Collected{Samples: ms(4, 1, 3, 2), Operations: 4, Errors: 2, Bytes: 4 * mib},
			elapsed: 2 * time.Second,
			want: Summary{
				Operations:      4,
				Errors:          2,
				Bytes:           4 * mib,
				DurationSeconds: 2,
				OpsPerSecond:    2,
				MiBPerSecond:    2,
				LatencyMs:       Latency{Min: 1, Mean: 2.5, P50: 2, P95: 4, P99: 4, Max: 4},
			},
		},
		{
			name:    "zero elapsed yields zero rates without panicking",
			in:      Collected{Samples: ms(1), Operations: 1, Bytes: mib},
			elapsed: 0,
			want: Summary{
				Operations:   1,
				Bytes:        mib,
				LatencyMs:    Latency{Min: 1, Mean: 1, P50: 1, P95: 1, P99: 1, Max: 1},
				OpsPerSecond: 0,
			},
		},
		{
			name:    "negative elapsed is treated as zero",
			in:      Collected{Samples: ms(1), Operations: 1},
			elapsed: -time.Second,
			want: Summary{
				Operations: 1,
				LatencyMs:  Latency{Min: 1, Mean: 1, P50: 1, P95: 1, P99: 1, Max: 1},
			},
		},
		{
			name:    "empty input",
			in:      Collected{},
			elapsed: time.Second,
			want:    Summary{DurationSeconds: 1},
		},
		{
			name:    "dropped samples count as operations but not latency",
			in:      Collected{Samples: ms(2, 4), Operations: 4, Dropped: 2},
			elapsed: time.Second,
			want: Summary{
				Operations:      4,
				DroppedSamples:  2,
				DurationSeconds: 1,
				OpsPerSecond:    4,
				LatencyMs:       Latency{Min: 2, Mean: 3, P50: 2, P95: 4, P99: 4, Max: 4},
			},
		},
		{
			name:    "all samples dropped leaves latency at zero",
			in:      Collected{Operations: 3, Dropped: 3},
			elapsed: time.Second,
			want: Summary{
				Operations:      3,
				DroppedSamples:  3,
				DurationSeconds: 1,
				OpsPerSecond:    3,
			},
		},
		{
			name:    "fractional MiB per second",
			in:      Collected{Samples: ms(1), Operations: 1, Bytes: mib},
			elapsed: 500 * time.Millisecond,
			want: Summary{
				Operations:      1,
				Bytes:           mib,
				DurationSeconds: 0.5,
				OpsPerSecond:    2,
				MiBPerSecond:    2,
				LatencyMs:       Latency{Min: 1, Mean: 1, P50: 1, P95: 1, P99: 1, Max: 1},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Summarize(tc.in, tc.elapsed)
			if got != tc.want {
				t.Fatalf("Summarize() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSummaryJSONGolden(t *testing.T) {
	s := Summarize(Collected{Samples: ms(4, 1, 3, 2), Operations: 4, Errors: 2, Bytes: 4 * mib}, 2*time.Second)
	got, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	const want = `{"operations":4,"errors":2,"dropped_samples":0,"bytes":4194304,"duration_seconds":2,` +
		`"ops_per_second":2,"mib_per_second":2,` +
		`"latency_ms":{"min":1,"mean":2.5,"p50":2,"p95":4,"p99":4,"max":4}}`
	if string(got) != want {
		t.Fatalf("JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

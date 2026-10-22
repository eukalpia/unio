package workload

import (
	"reflect"
	"testing"
)

// TestWorkerStreams checks the checked conversion from a worker count to the
// per-worker PCG stream selectors. A negative or oversized count must fail
// rather than wrap around.
func TestWorkerStreams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		n       int
		want    []uint64
		wantErr bool
	}{
		{name: "one worker", n: 1, want: []uint64{0}},
		{name: "three workers", n: 3, want: []uint64{0, 1, 2}},
		{name: "zero", n: 0, wantErr: true},
		{name: "negative", n: -1, wantErr: true},
		{name: "above the limit", n: maxWorkers + 1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := workerStreams(tc.n)
			if (err != nil) != tc.wantErr {
				t.Fatalf("workerStreams(%d) error = %v, wantErr %v", tc.n, err, tc.wantErr)
			}
			if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("workerStreams(%d) = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
}

// TestWorkerStreamsAtLimit checks the largest accepted count.
func TestWorkerStreamsAtLimit(t *testing.T) {
	t.Parallel()
	got, err := workerStreams(maxWorkers)
	if err != nil {
		t.Fatalf("workerStreams(maxWorkers): %v", err)
	}
	if len(got) != maxWorkers || got[maxWorkers-1] != maxWorkers-1 {
		t.Fatalf("workerStreams(maxWorkers) has %d entries, last %d", len(got), got[len(got)-1])
	}
}

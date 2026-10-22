package workload

import "fmt"

// maxWorkers bounds the number of worker goroutines in one run.
const maxWorkers = 4096

// workerStreams returns the PCG stream selector of each worker, 0 to n-1, so
// that every worker draws its own deterministic sequence from the run seed.
//
// The count is checked before any conversion: a negative or oversized value
// is an error here, rather than a wrapped-around stream selector.
func workerStreams(n int) ([]uint64, error) {
	if n < 1 || n > maxWorkers {
		return nil, fmt.Errorf("worker count must be between 1 and %d, got %d", maxWorkers, n)
	}
	streams := make([]uint64, 0, n)
	for i := range n {
		if i < 0 {
			return nil, fmt.Errorf("worker index %d is negative", i)
		}
		streams = append(streams, uint64(i))
	}
	return streams, nil
}

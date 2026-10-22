// Package workload defines the benchmark workloads, runs them with a fixed
// concurrency and measurement window, and reports per-operation statistics.
package workload

import (
	"context"
	"math/rand/v2"
)

// OpSpec names one operation type that a workload issues, with the weight
// used to choose it at random. Weights must be at least 1.
type OpSpec struct {
	Name   string
	Weight int
}

// Worker issues operations for one goroutine. Every goroutine gets its own
// Worker, so a Worker may keep per-goroutine state such as a pagination cursor
// and needs no locking.
type Worker interface {
	// Do performs operation op, an index into Spec.Ops. On success it returns
	// the payload bytes moved; metadata-only operations return 0.
	Do(ctx context.Context, op int, rng *rand.Rand) (int64, error)
}

// Spec describes one workload.
type Spec struct {
	// Name is the workload name, for example put-4k.
	Name string
	// Ops lists the operation types in report order.
	Ops []OpSpec
	// Setup, when non-nil, prepares the bucket, for example by pre-creating
	// the objects a GET workload reads. It runs once per Run, before the
	// measurement window opens.
	Setup func(ctx context.Context) error
	// NewWorker returns the Worker for goroutine id.
	NewWorker func(id int) Worker
}

// pick chooses an operation index with probability proportional to its
// weight. A single operation needs no random draw.
func pick(rng *rand.Rand, ops []OpSpec) int {
	if len(ops) == 1 {
		return 0
	}
	total := 0
	for _, o := range ops {
		total += o.Weight
	}
	r := rng.IntN(total)
	for i, o := range ops {
		if r < o.Weight {
			return i
		}
		r -= o.Weight
	}
	return len(ops) - 1
}

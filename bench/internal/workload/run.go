package workload

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/eukalpia/unio/bench/internal/stats"
)

// TotalName labels the combined result of a workload with several operation
// types.
const TotalName = "TOTAL"

// RunConfig controls one run of a workload.
type RunConfig struct {
	// Concurrency is the number of worker goroutines. It must be at least 1.
	Concurrency int
	// Warmup is the time before measurement starts. Operations that start
	// during warmup are executed but not measured.
	Warmup time.Duration
	// Duration is the length of the measurement window. It must be positive.
	Duration time.Duration
	// Seed makes the operation and key choices reproducible.
	Seed uint64
	// MaxOpsPerWorker, when positive, stops each worker after that many
	// operations, warmup included. Tests use it to run to a fixed count; the
	// CLI leaves it zero.
	MaxOpsPerWorker int64
}

// OperationResult is the summary of one operation type.
type OperationResult struct {
	Operation string
	Summary   stats.Summary
}

// Result is the outcome of one workload run.
type Result struct {
	Name string
	// Interrupted reports that the context was cancelled. The results then
	// cover the part of the window that completed before cancellation.
	Interrupted bool
	// Operations has one entry per operation type, followed by a TOTAL entry
	// when the workload has more than one operation type.
	Operations []OperationResult
}

// Run executes spec: its optional Setup, then Warmup followed by Duration with
// cfg.Concurrency workers. Setup failures are returned as errors. Cancelling
// ctx stops the workers and returns the results collected so far with
// Interrupted set. Individual operation errors are counted, not returned.
func Run(ctx context.Context, spec Spec, cfg RunConfig) (Result, error) {
	if err := validate(spec, cfg); err != nil {
		return Result{}, err
	}
	if spec.Setup != nil {
		if err := spec.Setup(ctx); err != nil {
			if ctx.Err() != nil {
				return finish(spec, nil, 0, true), nil
			}
			return Result{}, fmt.Errorf("setup %s: %w", spec.Name, err)
		}
	}
	if ctx.Err() != nil {
		return finish(spec, nil, 0, true), nil
	}

	win := window{start: time.Now().Add(cfg.Warmup)}
	win.end = win.start.Add(cfg.Duration)
	runCtx, cancel := context.WithDeadline(ctx, win.end)
	defer cancel()

	// Each worker owns its recorders, so recording needs no locks. The
	// sample limit keeps total memory bounded whatever the throughput.
	streams, err := workerStreams(cfg.Concurrency)
	if err != nil {
		return Result{}, err
	}
	limit := stats.SampleLimit(cfg.Concurrency * len(spec.Ops))
	recs := make([][]*stats.Recorder, cfg.Concurrency)
	var wg sync.WaitGroup
	for id := range cfg.Concurrency {
		recs[id] = newRecorders(len(spec.Ops), limit)
		wg.Add(1)
		go func(id int, own []*stats.Recorder) {
			defer wg.Done()
			// #nosec G404 -- benchmark workload generation, not security sensitive
			rng := rand.New(rand.NewPCG(cfg.Seed, streams[id]))
			runWorker(runCtx, win, spec.NewWorker(id), spec.Ops, own, rng, cfg.MaxOpsPerWorker)
		}(id, recs[id])
	}
	wg.Wait()

	return finish(spec, recs, win.elapsed(time.Now()), ctx.Err() != nil), nil
}

// validate rejects configurations that cannot produce a meaningful run.
func validate(spec Spec, cfg RunConfig) error {
	switch {
	case spec.Name == "":
		return errors.New("workload has no name")
	case spec.NewWorker == nil:
		return fmt.Errorf("workload %s has no worker factory", spec.Name)
	case len(spec.Ops) == 0:
		return fmt.Errorf("workload %s has no operations", spec.Name)
	case cfg.Concurrency < 1:
		return fmt.Errorf("concurrency must be at least 1, got %d", cfg.Concurrency)
	case cfg.Duration <= 0:
		return fmt.Errorf("duration must be positive, got %v", cfg.Duration)
	case cfg.Warmup < 0:
		return fmt.Errorf("warmup must not be negative, got %v", cfg.Warmup)
	}
	for _, o := range spec.Ops {
		if o.Weight < 1 {
			return fmt.Errorf("operation %s of workload %s has weight %d, want at least 1", o.Name, spec.Name, o.Weight)
		}
	}
	return nil
}

// newRecorders returns one recorder per operation type for a single worker.
func newRecorders(ops, limit int) []*stats.Recorder {
	out := make([]*stats.Recorder, ops)
	for i := range out {
		out[i] = stats.NewRecorder(limit)
	}
	return out
}

// runWorker issues operations until ctx is done or maxOps is reached, and
// records each outcome that falls inside the measurement window.
func runWorker(ctx context.Context, win window, w Worker, ops []OpSpec, recs []*stats.Recorder, rng *rand.Rand, maxOps int64) {
	for done := int64(0); maxOps <= 0 || done < maxOps; done++ {
		if ctx.Err() != nil {
			return
		}
		op := pick(rng, ops)
		start := time.Now()
		n, err := w.Do(ctx, op, rng)
		end := time.Now()
		switch win.verdict(start, end, err, ctx.Err() != nil) {
		case verdictSuccess:
			recs[op].Success(end.Sub(start), n)
		case verdictFailure:
			recs[op].Failure()
		case verdictDiscard:
		}
	}
}

// finish merges the recorders of every worker into per-operation summaries,
// plus a TOTAL entry when there is more than one operation type. recs may be
// nil, which yields zero-valued summaries.
func finish(spec Spec, recs [][]*stats.Recorder, elapsed time.Duration, interrupted bool) Result {
	perOp := make([]stats.Collected, len(spec.Ops))
	for op := range spec.Ops {
		column := make([]*stats.Recorder, 0, len(recs))
		for _, worker := range recs {
			if op < len(worker) {
				column = append(column, worker[op])
			}
		}
		perOp[op] = stats.Merge(column...)
	}

	res := Result{Name: spec.Name, Interrupted: interrupted}
	for op, o := range spec.Ops {
		res.Operations = append(res.Operations, OperationResult{
			Operation: o.Name,
			Summary:   stats.Summarize(perOp[op], elapsed),
		})
	}
	if len(spec.Ops) > 1 {
		res.Operations = append(res.Operations, OperationResult{
			Operation: TotalName,
			Summary:   stats.Summarize(stats.Combine(perOp...), elapsed),
		})
	}
	return res
}

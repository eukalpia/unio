// Package harness runs a list of workloads against one store in order and
// assembles the report. It owns no I/O except the optional progress writer.
package harness

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/eukalpia/unio/bench/internal/objstore"
	"github.com/eukalpia/unio/bench/internal/report"
	"github.com/eukalpia/unio/bench/internal/workload"
)

// Options configures a run. Endpoint is recorded in the report as given, so
// the caller must pass it without credentials.
type Options struct {
	Endpoint  string
	Region    string
	PathStyle bool
	Bucket    string
	// Workloads are validated workload names in the order to run them.
	Workloads   []string
	Concurrency int
	Warmup      time.Duration
	Duration    time.Duration
	ListKeys    int
	ListPage    int
	Seed        uint64
	// Progress, when non-nil, receives one line as each workload finishes.
	Progress io.Writer
}

// Run ensures the bucket exists, runs each workload in order and returns the
// report. A cancelled context stops the run early and is not an error: the
// report is returned with Interrupted set. Any other failure returns the
// partial report together with the error.
func Run(ctx context.Context, store objstore.Store, opts Options) (rep report.Report, err error) {
	rep = report.Report{
		SchemaVersion: report.SchemaVersion,
		Tool:          report.ToolName,
		StartedAt:     time.Now().UTC(),
		Environment: report.Environment{
			GoVersion: runtime.Version(),
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			NumCPU:    runtime.NumCPU(),
		},
		Config: report.Config{
			Endpoint:        opts.Endpoint,
			Region:          opts.Region,
			PathStyle:       opts.PathStyle,
			Bucket:          opts.Bucket,
			Workloads:       append([]string(nil), opts.Workloads...),
			Concurrency:     opts.Concurrency,
			DurationSeconds: opts.Duration.Seconds(),
			WarmupSeconds:   opts.Warmup.Seconds(),
			ListKeys:        opts.ListKeys,
			ListPage:        opts.ListPage,
			Seed:            opts.Seed,
		},
	}
	defer func() { rep.FinishedAt = time.Now().UTC() }()

	if err = store.EnsureBucket(ctx, opts.Bucket); err != nil {
		if ctx.Err() != nil {
			rep.Interrupted = true
			return rep, nil
		}
		return rep, fmt.Errorf("prepare bucket: %w", err)
	}

	env := workload.Env{
		Store:       store,
		Bucket:      opts.Bucket,
		Concurrency: opts.Concurrency,
		Seed:        opts.Seed,
		ListKeys:    opts.ListKeys,
		ListPage:    opts.ListPage,
	}
	cfg := workload.RunConfig{
		Concurrency: opts.Concurrency,
		Warmup:      opts.Warmup,
		Duration:    opts.Duration,
		Seed:        opts.Seed,
	}
	for _, name := range opts.Workloads {
		if ctx.Err() != nil {
			rep.Interrupted = true
			break
		}
		var res workload.Result
		res, err = runOne(ctx, name, env, cfg)
		if err != nil {
			return rep, err
		}
		rep.Workloads = append(rep.Workloads, toReport(res))
		progress(opts.Progress, res)
		if res.Interrupted {
			rep.Interrupted = true
			break
		}
	}
	return rep, nil
}

// runOne builds and runs a single workload.
func runOne(ctx context.Context, name string, env workload.Env, cfg workload.RunConfig) (workload.Result, error) {
	spec, err := workload.Build(name, env)
	if err != nil {
		return workload.Result{}, err
	}
	res, err := workload.Run(ctx, spec, cfg)
	if err != nil {
		return workload.Result{}, fmt.Errorf("workload %s: %w", name, err)
	}
	return res, nil
}

// toReport converts a workload result to its report form.
func toReport(res workload.Result) report.Workload {
	out := report.Workload{Name: res.Name}
	for _, op := range res.Operations {
		out.Operations = append(out.Operations, report.Operation{Operation: op.Operation, Summary: op.Summary})
	}
	return out
}

// progress writes a one-line summary of a finished workload when a writer is
// configured. Write errors are ignored: progress is advisory.
func progress(w io.Writer, res workload.Result) {
	if w == nil {
		return
	}
	for _, op := range res.Operations {
		s := op.Summary
		_, _ = fmt.Fprintf(w, "%s %s: ops=%d errors=%d p99=%.3fms\n",
			res.Name, op.Operation, s.Operations, s.Errors, s.LatencyMs.P99)
	}
}

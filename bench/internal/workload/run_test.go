package workload

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// funcWorker adapts a function to Worker for tests.
type funcWorker func(ctx context.Context, op int, rng *rand.Rand) (int64, error)

func (f funcWorker) Do(ctx context.Context, op int, rng *rand.Rand) (int64, error) {
	return f(ctx, op, rng)
}

func testSpec(ops []OpSpec, fn funcWorker) Spec {
	return Spec{
		Name:      "test",
		Ops:       ops,
		NewWorker: func(int) Worker { return fn },
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	ok := testSpec([]OpSpec{{Name: "A", Weight: 1}}, func(context.Context, int, *rand.Rand) (int64, error) { return 0, nil })
	tests := []struct {
		name string
		spec Spec
		cfg  RunConfig
	}{
		{name: "zero concurrency", spec: ok, cfg: RunConfig{Concurrency: 0, Duration: 1}},
		{name: "zero duration", spec: ok, cfg: RunConfig{Concurrency: 1, Duration: 0}},
		{name: "negative warmup", spec: ok, cfg: RunConfig{Concurrency: 1, Duration: 1, Warmup: -1}},
		{name: "no operations", spec: Spec{Name: "x", NewWorker: ok.NewWorker}, cfg: RunConfig{Concurrency: 1, Duration: 1}},
		{name: "zero weight", spec: testSpec([]OpSpec{{Name: "A", Weight: 0}}, nil), cfg: RunConfig{Concurrency: 1, Duration: 1}},
		{name: "no worker factory", spec: Spec{Name: "x", Ops: []OpSpec{{Name: "A", Weight: 1}}}, cfg: RunConfig{Concurrency: 1, Duration: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Run(context.Background(), tc.spec, tc.cfg); err == nil {
				t.Fatal("Run returned no error")
			}
		})
	}
}

func TestRunSetupErrorIsReturned(t *testing.T) {
	boom := errors.New("prefill failed")
	spec := testSpec([]OpSpec{{Name: "A", Weight: 1}}, func(context.Context, int, *rand.Rand) (int64, error) { return 0, nil })
	spec.Setup = func(context.Context) error { return boom }

	res, err := Run(context.Background(), spec, RunConfig{Concurrency: 1, Duration: 1})
	if !errors.Is(err, boom) {
		t.Fatalf("Run error = %v, want wrapping %v", err, boom)
	}
	if res.Name != "" || len(res.Operations) != 0 {
		t.Fatalf("Run returned a result after setup failure: %+v", res)
	}
}

func TestRunAlreadyCancelledIsInterrupted(t *testing.T) {
	var calls atomic.Int64
	spec := testSpec([]OpSpec{{Name: "A", Weight: 1}}, func(context.Context, int, *rand.Rand) (int64, error) {
		calls.Add(1)
		return 1, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := Run(ctx, spec, RunConfig{Concurrency: 2, Duration: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Interrupted {
		t.Fatal("Interrupted = false for a cancelled context")
	}
	if calls.Load() != 0 {
		t.Fatalf("worker ran %d times after cancellation", calls.Load())
	}
	if got := res.Operations[0].Summary.Operations; got != 0 {
		t.Fatalf("operations = %d, want 0", got)
	}
}

func TestRunCancellationKeepsCollectedResults(t *testing.T) {
	const cancelAfter = 50
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int64
	spec := testSpec([]OpSpec{{Name: "A", Weight: 1}}, func(context.Context, int, *rand.Rand) (int64, error) {
		if calls.Add(1) == cancelAfter {
			cancel()
		}
		return 8, nil
	})

	res, err := Run(ctx, spec, RunConfig{Concurrency: 1, Duration: time.Hour})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Interrupted {
		t.Fatal("Interrupted = false after cancellation")
	}
	got := res.Operations[0].Summary
	if got.Operations < cancelAfter || got.Operations > cancelAfter+1 {
		t.Fatalf("operations = %d, want about %d", got.Operations, cancelAfter)
	}
	if got.Bytes != got.Operations*8 {
		t.Fatalf("bytes = %d, want %d", got.Bytes, got.Operations*8)
	}
}

func TestRunMaxOpsPerWorkerStopsEachWorker(t *testing.T) {
	var calls atomic.Int64
	spec := testSpec([]OpSpec{{Name: "A", Weight: 1}}, func(context.Context, int, *rand.Rand) (int64, error) {
		calls.Add(1)
		return 0, nil
	})
	res, err := Run(context.Background(), spec, RunConfig{Concurrency: 3, Duration: time.Hour, MaxOpsPerWorker: 100})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Interrupted {
		t.Fatal("Interrupted = true for a run that finished")
	}
	if calls.Load() != 300 {
		t.Fatalf("worker calls = %d, want 300", calls.Load())
	}
	if got := res.Operations[0].Summary.Operations; got != 300 {
		t.Fatalf("operations = %d, want 300", got)
	}
}

func TestRunErrorsDoNotStopTheRun(t *testing.T) {
	var calls atomic.Int64
	boom := errors.New("transient")
	spec := testSpec([]OpSpec{{Name: "A", Weight: 1}}, func(context.Context, int, *rand.Rand) (int64, error) {
		if calls.Add(1)%2 == 0 {
			return 0, boom
		}
		return 4, nil
	})
	res, err := Run(context.Background(), spec, RunConfig{Concurrency: 1, Duration: time.Hour, MaxOpsPerWorker: 100})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := res.Operations[0].Summary
	if got.Operations != 50 || got.Errors != 50 || got.Bytes != 200 {
		t.Fatalf("ops %d errors %d bytes %d, want 50 50 200", got.Operations, got.Errors, got.Bytes)
	}
}

func TestRunReportsEachOperationAndTotal(t *testing.T) {
	spec := testSpec(
		[]OpSpec{{Name: "GET", Weight: 1}, {Name: "PUT", Weight: 1}},
		func(_ context.Context, op int, _ *rand.Rand) (int64, error) {
			if op == 0 {
				return 10, nil
			}
			return 100, nil
		},
	)
	res, err := Run(context.Background(), spec, RunConfig{Concurrency: 1, Duration: time.Hour, MaxOpsPerWorker: 40})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Operations) != 3 {
		t.Fatalf("got %d operation results, want GET, PUT and TOTAL", len(res.Operations))
	}
	names := []string{res.Operations[0].Operation, res.Operations[1].Operation, res.Operations[2].Operation}
	if names[0] != "GET" || names[1] != "PUT" || names[2] != "TOTAL" {
		t.Fatalf("operation names = %v", names)
	}
	total := res.Operations[2].Summary
	if total.Operations != res.Operations[0].Summary.Operations+res.Operations[1].Summary.Operations {
		t.Fatalf("TOTAL operations = %d, not the sum of the parts", total.Operations)
	}
	if total.Bytes != res.Operations[0].Summary.Bytes+res.Operations[1].Summary.Bytes {
		t.Fatalf("TOTAL bytes = %d, not the sum of the parts", total.Bytes)
	}
}

func TestRunSingleOperationHasNoTotal(t *testing.T) {
	spec := testSpec([]OpSpec{{Name: "PUT", Weight: 1}}, func(context.Context, int, *rand.Rand) (int64, error) { return 0, nil })
	res, err := Run(context.Background(), spec, RunConfig{Concurrency: 1, Duration: time.Hour, MaxOpsPerWorker: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Operations) != 1 || res.Operations[0].Operation != "PUT" {
		t.Fatalf("operations = %+v, want a single PUT entry", res.Operations)
	}
}

package harness

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/bench/internal/fakestore"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func baseOptions(workloads ...string) Options {
	return Options{
		Endpoint:    "http://localhost:9000",
		Region:      "us-east-1",
		PathStyle:   true,
		Bucket:      "unio-bench-test",
		Workloads:   workloads,
		Concurrency: 2,
		Duration:    50 * time.Millisecond,
		ListKeys:    50,
		ListPage:    10,
		Seed:        1,
	}
}

func TestRunReportsEveryWorkloadInOrder(t *testing.T) {
	var progress bytes.Buffer
	opts := baseOptions("put-4k", "get-4k", "list", "mixed")
	opts.Progress = &progress

	rep, err := Run(context.Background(), fakestore.New(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Interrupted {
		t.Fatal("Interrupted = true for a completed run")
	}
	if rep.SchemaVersion != 1 || rep.Tool != "unio-bench" {
		t.Fatalf("schema = %d tool = %q", rep.SchemaVersion, rep.Tool)
	}
	if rep.FinishedAt.Before(rep.StartedAt) {
		t.Fatal("FinishedAt precedes StartedAt")
	}
	if rep.Config.Bucket != "unio-bench-test" || rep.Config.Concurrency != 2 || rep.Config.DurationSeconds != 0.05 {
		t.Fatalf("config not echoed: %+v", rep.Config)
	}

	wantNames := []string{"put-4k", "get-4k", "list", "mixed"}
	if len(rep.Workloads) != len(wantNames) {
		t.Fatalf("got %d workloads, want %d", len(rep.Workloads), len(wantNames))
	}
	for i, name := range wantNames {
		if rep.Workloads[i].Name != name {
			t.Fatalf("workload %d = %q, want %q", i, rep.Workloads[i].Name, name)
		}
		if len(rep.Workloads[i].Operations) == 0 {
			t.Fatalf("workload %s has no operations", name)
		}
	}
	if ops := rep.Workloads[0].Operations[0].Summary.Operations; ops == 0 {
		t.Fatal("put-4k recorded no operations")
	}
	for _, want := range []string{"put-4k PUT:", "get-4k GET:", "list LIST_DELIMITER:", "mixed TOTAL:"} {
		if !strings.Contains(progress.String(), want) {
			t.Errorf("progress output lacks %q:\n%s", want, progress.String())
		}
	}
}

func TestRunCancelledBeforeStartIsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep, err := Run(ctx, fakestore.New(), baseOptions("put-4k"))
	if err != nil {
		t.Fatalf("Run returned an error for cancellation: %v", err)
	}
	if !rep.Interrupted {
		t.Fatal("Interrupted = false")
	}
	if len(rep.Workloads) != 0 {
		t.Fatalf("got %d workloads after cancellation, want 0", len(rep.Workloads))
	}
}

func TestRunCancelMidWorkloadKeepsPartialResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var puts atomic.Int64
	store := fakestore.New()
	store.After = func(call, _ string) error {
		if call == fakestore.CallPut && puts.Add(1) == 20 {
			cancel()
		}
		return nil
	}
	opts := baseOptions("put-4k", "get-4k")
	opts.Duration = time.Hour

	rep, err := Run(ctx, store, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Interrupted {
		t.Fatal("Interrupted = false after cancellation")
	}
	if len(rep.Workloads) != 1 || rep.Workloads[0].Name != "put-4k" {
		t.Fatalf("workloads = %+v, want only the interrupted put-4k", rep.Workloads)
	}
	if got := rep.Workloads[0].Operations[0].Summary.Operations; got < 20 {
		t.Fatalf("collected %d operations before cancellation, want at least 20", got)
	}
}

func TestRunUnknownWorkloadReturnsPartialReport(t *testing.T) {
	rep, err := Run(context.Background(), fakestore.New(), baseOptions("put-4k", "bogus"))
	if err == nil {
		t.Fatal("unknown workload was accepted")
	}
	if len(rep.Workloads) != 1 || rep.Workloads[0].Name != "put-4k" {
		t.Fatalf("partial report = %+v, want the completed put-4k", rep.Workloads)
	}
}

func TestRunBucketFailureIsReturned(t *testing.T) {
	boom := errors.New("access denied")
	store := fakestore.New()
	store.Fail = func(call, _ string) error {
		if call == fakestore.CallBucket {
			return boom
		}
		return nil
	}
	rep, err := Run(context.Background(), store, baseOptions("put-4k"))
	if !errors.Is(err, boom) {
		t.Fatalf("Run error = %v, want %v", err, boom)
	}
	if rep.Interrupted || len(rep.Workloads) != 0 {
		t.Fatalf("report = %+v, want no workloads and not interrupted", rep)
	}
}

package workload

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/bench/internal/fakestore"
	"github.com/eukalpia/unio/bench/internal/keys"
	"github.com/eukalpia/unio/bench/internal/payload"
)

func testEnv(store *fakestore.Store) Env {
	return Env{Store: store, Bucket: "bench", Concurrency: 2, Seed: 1, ListKeys: 100, ListPage: 10}
}

func findOp(t *testing.T, res Result, name string) OperationResult {
	t.Helper()
	for _, o := range res.Operations {
		if o.Operation == name {
			return o
		}
	}
	t.Fatalf("no operation %q in %+v", name, res.Operations)
	return OperationResult{}
}

func TestObjectWorkloadsPayloadBookkeeping(t *testing.T) {
	tests := []struct {
		name      string
		wantBytes int64
		op        string
	}{
		{name: "put-4k", wantBytes: 4 << 10, op: "PUT"},
		{name: "get-4k", wantBytes: 4 << 10, op: "GET"},
		{name: "put-1m", wantBytes: 1 << 20, op: "PUT"},
		{name: "get-1m", wantBytes: 1 << 20, op: "GET"},
		{name: "put-64m", wantBytes: 64 << 20, op: "PUT"},
		{name: "get-64m", wantBytes: 64 << 20, op: "GET"},
	}
	const perWorker, workers = 3, 2
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := fakestore.New()
			spec, err := Build(tc.name, testEnv(store))
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			res, err := Run(context.Background(), spec, RunConfig{
				Concurrency:     workers,
				Duration:        time.Hour,
				Seed:            1,
				MaxOpsPerWorker: perWorker,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := findOp(t, res, tc.op).Summary
			if got.Operations != perWorker*workers || got.Errors != 0 {
				t.Fatalf("ops %d errors %d, want %d and 0", got.Operations, got.Errors, perWorker*workers)
			}
			if want := got.Operations * tc.wantBytes; got.Bytes != want {
				t.Fatalf("bytes = %d, want %d (ops * object size)", got.Bytes, want)
			}
		})
	}
}

func TestGetPrefillsWholeKeyspace(t *testing.T) {
	store := fakestore.New()
	spec, err := Build("get-4k", testEnv(store))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if spec.Setup == nil {
		t.Fatal("get-4k has no setup; GET would read missing keys")
	}
	if err := spec.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if got := store.Objects(); got != 1000 {
		t.Fatalf("objects after prefill = %d, want 1000", got)
	}
	for _, k := range store.Keys() {
		if !strings.HasPrefix(k, "bench/4k/") {
			t.Fatalf("key %q is outside the 4 KiB keyspace", k)
		}
		if n, _ := store.Size(k); n != 4096 {
			t.Fatalf("object %q has size %d, want 4096", k, n)
		}
	}
}

func mustKeyspace(t *testing.T, prefix string, n int) keys.Keyspace {
	t.Helper()
	ks, err := keys.NewKeyspace(prefix, n)
	if err != nil {
		t.Fatalf("NewKeyspace: %v", err)
	}
	return ks
}

func TestGetWorkerRejectsShortBody(t *testing.T) {
	store := fakestore.New()
	ks := mustKeyspace(t, "bench/4k/", 1)
	if err := store.PutObject(context.Background(), "bench", ks.Key(0), make([]byte, 5)); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	buf, err := payload.NewBuffer(4096, 1)
	if err != nil {
		t.Fatalf("NewBuffer: %v", err)
	}
	w := &objectWorker{store: store, bucket: "bench", space: ks, buf: buf, kinds: []opKind{kindGet}}

	n, err := w.Do(context.Background(), 0, rand.New(rand.NewPCG(1, 1)))
	if err == nil {
		t.Fatal("Do accepted a 5-byte body for a 4096-byte object")
	}
	if n != 0 {
		t.Fatalf("bytes = %d for a failed GET, want 0", n)
	}
}

func TestInjectedFailuresAreCountedAndRunContinues(t *testing.T) {
	errInjected := errors.New("injected failure")
	store := fakestore.New()
	var puts int
	store.Fail = func(call, _ string) error {
		if call != fakestore.CallPut {
			return nil
		}
		puts++
		if puts%3 == 0 {
			return errInjected
		}
		return nil
	}
	spec, err := Build("put-4k", testEnv(store))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	res, err := Run(context.Background(), spec, RunConfig{Concurrency: 1, Duration: time.Hour, Seed: 1, MaxOpsPerWorker: 300})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := findOp(t, res, "PUT").Summary
	if got.Errors != 100 || got.Operations != 200 {
		t.Fatalf("errors %d ops %d, want 100 and 200", got.Errors, got.Operations)
	}
	if calls := store.Count(fakestore.CallPut); calls != 300 {
		t.Fatalf("PUT calls = %d, want 300 (the run must not stop on errors)", calls)
	}
}

func TestMixedRatioWithFixedSeed(t *testing.T) {
	const total = 10000
	store := fakestore.New()
	spec, err := Build("mixed", testEnv(store))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	const prefill = 1000 // keys in the 4 KiB keyspace, written by Setup
	res, err := Run(context.Background(), spec, RunConfig{Concurrency: 1, Duration: time.Hour, Seed: 1, MaxOpsPerWorker: total})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	gets := findOp(t, res, "GET").Summary
	puts := findOp(t, res, "PUT").Summary
	sum := findOp(t, res, "TOTAL").Summary

	if gets.Operations+puts.Operations != total || sum.Operations != total {
		t.Fatalf("GET %d + PUT %d != TOTAL %d (want %d)", gets.Operations, puts.Operations, sum.Operations, total)
	}
	share := float64(gets.Operations) / total
	if math.Abs(share-0.70) > 0.02 {
		t.Fatalf("GET share = %.4f, want 0.70 within 0.02", share)
	}
	if calls := store.Count(fakestore.CallGet); calls != gets.Operations {
		t.Fatalf("GET calls = %d, summary says %d", calls, gets.Operations)
	}
	// Setup pre-creates the keyspace with PUTs, so the PUT calls made by the
	// run itself are the total minus the prefill.
	if calls := store.Count(fakestore.CallPut) - prefill; calls != puts.Operations {
		t.Fatalf("PUT calls during run = %d, summary says %d", calls, puts.Operations)
	}
	if gets.Errors+puts.Errors != 0 {
		t.Fatalf("errors = %d, want 0", gets.Errors+puts.Errors)
	}
}

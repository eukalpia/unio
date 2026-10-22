package engine_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3err"
)

// waitUntil polls cond until it holds or the test's patience runs out.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestSharedBudgetChargesInlinePuts checks that Options.Budget replaces the
// private budget: the buffer of a running put is charged to the shared budget,
// and the charge is gone when the put returns. BufferBudget is ignored.
func TestSharedBudgetChargesInlinePuts(t *testing.T) {
	const inline = 4096
	budget := bufpool.NewBudget(1 << 20)
	h := newHarness(t, engine.Options{InlineThreshold: inline, Budget: budget, BufferBudget: 1})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	start := budget.InUse()

	body := &gatedBody{data: payloadOf(1000, 1), gate: make(chan struct{}), started: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := h.PutObject(ctx, testBucket, "k", body, object.PutOptions{})
		done <- err
	}()
	select {
	case <-body.started:
	case <-time.After(10 * time.Second):
		t.Fatal("put never read its body")
	}
	if got := budget.InUse(); got != start+inline {
		t.Fatalf("InUse during put = %d, want %d", got, start+inline)
	}
	close(body.gate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("put did not finish")
	}
	if got := budget.InUse(); got != start {
		t.Fatalf("InUse after put = %d, want the starting value %d", got, start)
	}
}

// TestTinySharedBudgetPutsWaitWithoutDeadlock runs concurrent inline puts on a
// budget that holds one buffer. They must queue behind one another, never
// exceed the budget, and all succeed.
func TestTinySharedBudgetPutsWaitWithoutDeadlock(t *testing.T) {
	const inline = 1024
	budget := bufpool.NewBudget(inline)
	h := newHarness(t, engine.Options{InlineThreshold: inline, Budget: budget})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}

	// Every body blocks on its first read until gate closes, so the put that
	// holds the buffer keeps it while the others queue for it.
	gate := make(chan struct{})
	const n = 8
	errs := make(chan error, n)
	datas := make([][]byte, n)
	for i := range n {
		datas[i] = payloadOf(inline, byte(i))
		body := &gatedBody{data: datas[i], gate: gate, started: make(chan struct{})}
		key := fmt.Sprintf("k-%d", i)
		go func() {
			pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			_, err := h.PutObject(pctx, testBucket, key, body, object.PutOptions{})
			errs <- err
		}()
	}
	waitUntil(t, "a put to hold the buffer", func() bool { return budget.InUse() == inline })
	for range 20 {
		if u := budget.InUse(); u > budget.Total() {
			t.Fatalf("InUse %d exceeds the budget %d", u, budget.Total())
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)

	timeout := time.After(30 * time.Second)
	for range n {
		select {
		case err := <-errs:
			if err != nil {
				t.Errorf("put: %v", err)
			}
		case <-timeout:
			t.Fatal("puts did not finish: the shared budget deadlocked")
		}
	}
	if got := budget.InUse(); got != 0 {
		t.Fatalf("InUse after all puts = %d, want 0", got)
	}
	for i := range n {
		key := fmt.Sprintf("k-%d", i)
		if got := mustReadKey(t, h, key); !bytes.Equal(got, datas[i]) {
			t.Fatalf("%s: read back %d bytes that differ from the put", key, len(got))
		}
	}
}

// TestSharedBudgetWaitFailsCleanlyOnContext checks that a put waiting for a
// shared buffer gives up when its context ends, and commits nothing.
func TestSharedBudgetWaitFailsCleanlyOnContext(t *testing.T) {
	const inline = 1024
	budget := bufpool.NewBudget(inline)
	h := newHarness(t, engine.Options{InlineThreshold: inline, Budget: budget})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}

	gate := make(chan struct{})
	holder := &gatedBody{data: payloadOf(inline, 1), gate: gate, started: make(chan struct{})}
	holderErr := make(chan error, 1)
	go func() {
		_, err := h.PutObject(ctx, testBucket, "holder", holder, object.PutOptions{})
		holderErr <- err
	}()
	waitUntil(t, "the holder to take the buffer", func() bool { return budget.InUse() == inline })

	wctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := h.PutObject(wctx, testBucket, "waiter", objecttest.NewBody(payloadOf(inline, 2)), object.PutOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter error = %v, want the context deadline", err)
	}
	if _, err := h.HeadObject(ctx, testBucket, "waiter", object.GetOptions{}); !s3err.Is(err, s3err.NoSuchKey) {
		t.Fatalf("waiter that gave up was committed: %v", err)
	}

	close(gate)
	if err := <-holderErr; err != nil {
		t.Fatalf("holder: %v", err)
	}
	if got := budget.InUse(); got != 0 {
		t.Fatalf("InUse after the holder returned = %d, want 0", got)
	}
}

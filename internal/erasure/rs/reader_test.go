package rs

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/erasuretest"
)

func TestReadAfterCloseFails(t *testing.T) {
	s, _ := newTestSet(t, 2, 1, Options{})
	data := pseudo(100, 11)
	id, layout := mustWrite(t, s, data)
	rc, err := s.Read(testCtx(t), id, layout, int64(len(data)), 0, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := rc.Read(make([]byte, 10)); err == nil {
		t.Fatal("Read after Close returned no error")
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestCancelledReadDoesNotQueueHealthyShards checks that a read cancelled
// while its drives are slow reports nothing: the shards are not damaged.
func TestCancelledReadDoesNotQueueHealthyShards(t *testing.T) {
	s, mem := newTestSet(t, 2, 1, Options{})
	data := pseudo(3*erasure.DefaultStripeSize, 12)
	id, layout := mustWrite(t, s, data)
	for _, d := range mem {
		d.SetFaults(erasuretest.Faults{Latency: 500 * time.Millisecond})
	}
	ctx, cancel := context.WithCancel(context.Background())
	rc, err := s.Read(ctx, id, layout, int64(len(data)), 0, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := io.Copy(io.Discard, rc); !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancel: err = %v, want context.Canceled", err)
	}
	_ = rc.Close()
	if n := s.HealQueue().Len(); n != 0 {
		t.Fatalf("cancelled read queued %d healthy shards for repair", n)
	}
}

func TestReadOfEmptyRangeNeedsNoDrives(t *testing.T) {
	s, mem := newTestSet(t, 2, 1, Options{})
	id, layout := mustWrite(t, s, pseudo(100, 13))
	for _, d := range mem {
		d.SetFaults(erasuretest.Faults{Offline: true})
	}
	if got := mustReadAll(t, s, id, layout, 100, 40, 0); len(got) != 0 {
		t.Fatalf("empty range returned %d bytes", len(got))
	}
}

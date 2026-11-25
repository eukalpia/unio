package healer

import (
	"bytes"
	"context"
	"log/slog"
	"math/rand/v2"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/erasuretest"
	"github.com/eukalpia/unio/internal/erasure/rs"
	"github.com/eukalpia/unio/internal/ids"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// stripe is the stripe size of the test sets. It is small so that a test
// object spans several stripes without much data.
const stripe = 64 << 10

// newSet returns a real rs.Set over k+m in-memory drives, and the drives.
func newSet(t *testing.T, id ids.SetID, k, m int, opts rs.Options) (*rs.Set, []*erasuretest.Drive) {
	t.Helper()
	mem := make([]*erasuretest.Drive, k+m)
	drives := make([]drive.Drive, k+m)
	for i := range mem {
		mem[i] = erasuretest.NewDrive()
		drives[i] = mem[i]
	}
	if opts.StripeSize == 0 {
		opts.StripeSize = stripe
	}
	s, err := rs.New(id, drives, k, m, opts)
	if err != nil {
		t.Fatalf("rs.New(%d+%d): %v", k, m, err)
	}
	return s, mem
}

// layoutOf returns the layout of a set whose drives are all present. It is the
// layout a request needs for an object that was never written.
func layoutOf(s *rs.Set, mem []*erasuretest.Drive) erasure.Layout {
	k, m := s.Shape()
	l := erasure.Layout{
		Set:          s.ID(),
		DataShards:   k,
		ParityShards: m,
		StripeSize:   stripe,
		Drives:       make([]ids.DriveID, len(mem)),
	}
	for i, d := range mem {
		l.Drives[i] = d.ID()
	}
	return l
}

// pseudo returns n deterministic bytes.
func pseudo(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(rng.Uint32())
	}
	return buf
}

// waitFor polls cond until it holds. The test fails if it does not hold in
// time.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// runUntil runs h until cond holds, then cancels it and waits for Run to
// return. The test fails if Run does not return after the cancel.
func runUntil(t *testing.T, h *Healer, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Run(ctx)
	}()
	waitFor(t, "the healer", cond)
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// textLogger returns a debug-level logger that writes to buf.
func textLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

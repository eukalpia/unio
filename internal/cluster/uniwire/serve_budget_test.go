package uniwire

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
)

// TestStreamReplyDrawsFromServeBudget checks the server side: a reply body is
// copied through a buffer from the serve budget.
func TestStreamReplyDrawsFromServeBudget(t *testing.T) {
	const bufSize = 32 << 10
	serve := bufpool.NewBudget(1 << 20)
	n := newTestNet(t)
	a := n.node("a", Limits{StreamBufferSize: bufSize, ServeBudget: serve})
	b := n.dialOnly("b", Limits{})

	data := randomBytes(t, 64<<10+1000)
	body := newGatedReader(data)
	t.Cleanup(body.unblock)
	if err := a.RegisterStream("test.Gated", func(context.Context, *StreamRequest) (*StreamResponse, error) {
		return &StreamResponse{Body: body, BodyLen: int64(len(data))}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := ctxWithTimeout(t, 30*time.Second)

	rep, err := b.Stream(ctx, "a", "test.Gated", nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	awaitStarted(t, body, ctx)
	if got := serve.InUse(); got != bufSize {
		t.Fatalf("serve budget InUse = %d during the reply, want %d", got, bufSize)
	}

	body.unblock()
	if got := readReply(t, rep); !bytes.Equal(got, data) {
		t.Fatal("reply body differs from the handler's data")
	}
	if got := serve.InUse(); got != 0 {
		t.Fatalf("serve budget InUse = %d after the reply, want 0", got)
	}
}

// TestDrainTakesNoBudget holds every buffer of both budgets. A stream that
// does not read its request body must still be drained and answered, because
// the drain uses a fixed scratch buffer and no budget.
func TestDrainTakesNoBudget(t *testing.T) {
	const bufSize = 32 << 10
	outbound := bufpool.NewBudget(4 * bufSize)
	serve := bufpool.NewBudget(4 * bufSize)
	holdEveryBuffer(t, outbound, bufSize)
	holdEveryBuffer(t, serve, bufSize)

	n := newTestNet(t)
	a := n.node("a", Limits{StreamBufferSize: bufSize, StreamBudget: outbound, ServeBudget: serve})
	b := n.dialOnly("b", Limits{})
	if err := a.RegisterStream("test.Ignore", func(context.Context, *StreamRequest) (*StreamResponse, error) {
		return &StreamResponse{Payload: []byte("ignored")}, nil
	}); err != nil {
		t.Fatal(err)
	}

	data := randomBytes(t, 3*bufSize+123)
	ctx := ctxWithTimeout(t, 5*time.Second)
	rep, err := b.Stream(ctx, "a", "test.Ignore", nil, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("drain with both budgets held: %v", err)
	}
	if !bytes.Equal(rep.Payload, []byte("ignored")) {
		t.Fatalf("payload %q, want %q", rep.Payload, "ignored")
	}
}

// TestNewRejectsSameBudgetForBothDirections checks that one budget cannot
// serve both directions. A node that does so can deadlock: its outbound calls
// hold the buffers that its inbound replies need.
func TestNewRejectsSameBudgetForBothDirections(t *testing.T) {
	shared := bufpool.NewBudget(1 << 20)
	_, err := New(Config{Limits: Limits{StreamBufferSize: 32 << 10, StreamBudget: shared, ServeBudget: shared}})
	if err == nil {
		t.Fatal("New accepted one budget for both directions")
	}
}

// TestNewRejectsServeBudgetSmallerThanABuffer checks the inbound budget against
// the buffer size, as the outbound budget is checked.
func TestNewRejectsServeBudgetSmallerThanABuffer(t *testing.T) {
	small := bufpool.NewBudget(16 << 10)
	_, err := New(Config{Limits: Limits{StreamBufferSize: 32 << 10, ServeBudget: small}})
	if err == nil {
		t.Fatal("New accepted a serve budget smaller than one stream buffer")
	}
}

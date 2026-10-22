package uniwire

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
)

// holdEveryBuffer takes every buffer of size bytes that budget can hand out,
// so that any transfer which draws from budget must wait. The buffers are
// released when the test ends.
func holdEveryBuffer(t *testing.T, budget *bufpool.Budget, size int) {
	t.Helper()
	p := bufpool.NewPool(size, budget)
	var held []*bufpool.Buffer
	for {
		buf, ok := p.TryGet()
		if !ok {
			break
		}
		held = append(held, buf)
	}
	if len(held) == 0 {
		t.Fatal("budget has no buffer to hold")
	}
	t.Cleanup(func() {
		for _, buf := range held {
			buf.Release()
		}
	})
}

// TestServeIsNotBlockedByHeldOutboundBuffers holds the whole outbound budget,
// as it is held when this node's own calls wait for peers, and then serves
// two streams from the same transport. One reply carries a body, which
// needs a buffer to write. The other does not read its request body, so the
// transport must drain it. Both must complete.
func TestServeIsNotBlockedByHeldOutboundBuffers(t *testing.T) {
	const bufSize = 32 << 10
	outbound := bufpool.NewBudget(4 * bufSize)
	holdEveryBuffer(t, outbound, bufSize)

	n := newTestNet(t)
	a := n.node("a", Limits{StreamBufferSize: bufSize, StreamBudget: outbound})
	b := n.dialOnly("b", Limits{})
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterStream("test.Ignore", func(context.Context, *StreamRequest) (*StreamResponse, error) {
		return &StreamResponse{Payload: []byte("ignored")}, nil
	}); err != nil {
		t.Fatal(err)
	}

	data := randomBytes(t, 64<<10+1000)
	ctx := ctxWithTimeout(t, 5*time.Second)

	rep, err := b.Stream(ctx, "a", "test.Echo", []byte("meta"), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("reply with a held outbound budget: %v", err)
	}
	if got := readReply(t, rep); !bytes.Equal(got, data) {
		t.Fatal("reply body differs from the upload")
	}

	rep, err = b.Stream(ctx, "a", "test.Ignore", nil, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("drain with a held outbound budget: %v", err)
	}
	if !bytes.Equal(rep.Payload, []byte("ignored")) {
		t.Fatalf("payload %q, want %q", rep.Payload, "ignored")
	}
}

package uniwire

import (
	"context"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// TestCloseCancelsHandlers checks that Close cancels the context of every
// running handler, so a handler waiting on a budget, a drive or a peer stops
// at once. Without that, Close would wait out the stream timeout, minutes by
// default, and the node could not stop within its shutdown timeout.
func TestCloseCancelsHandlers(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})

	streamStarted := make(chan struct{})
	rpcStarted := make(chan struct{})
	if err := a.RegisterStream("test.Block", func(ctx context.Context, _ *StreamRequest) (*StreamResponse, error) {
		close(streamStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Register("test.Block", func(ctx context.Context, _ ids.NodeID, _ []byte) ([]byte, error) {
		close(rpcStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}

	ctx := ctxWithTimeout(t, 30*time.Second)
	go func() { _, _ = b.Stream(ctx, "a", "test.Block", nil, nil, 0) }()
	go func() { _ = b.Call(ctx, "a", "test.Block", &uniov1.DriveRequest{}, &uniov1.DriveRequest{}) }()
	for _, started := range []chan struct{}{streamStarted, rpcStarted} {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("handler did not start")
		}
	}

	closed := make(chan error, 1)
	begin := time.Now()
	go func() { closed <- a.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close is still waiting for handlers after 5s")
	}
	if d := time.Since(begin); d > 2*time.Second {
		t.Fatalf("Close took %v", d)
	}
}

package uniwire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// echoHandler returns the request bytes unchanged.
func echoHandler(_ context.Context, _ ids.NodeID, req []byte) ([]byte, error) {
	return req, nil
}

// ctx returns a test context that expires after d.
func ctxWithTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestCallRoundTripAndIdentity(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.Register("test.Who", func(_ context.Context, from ids.NodeID, _ []byte) ([]byte, error) {
		return (&uniov1.DriveRequest{Drive: []byte(from)}).MarshalVT()
	}); err != nil {
		t.Fatal(err)
	}
	var resp uniov1.DriveRequest
	if err := b.Call(ctxWithTimeout(t, 5*time.Second), "a", "test.Who", &uniov1.DriveRequest{}, &resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Drive) != "b" {
		t.Fatalf("handler saw caller %q, want b", resp.Drive)
	}
}

func TestCallEchoesPayload(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.Register("test.Echo", echoHandler); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("x"), 100000)
	var resp uniov1.DriveRequest
	if err := b.Call(ctxWithTimeout(t, 5*time.Second), "a", "test.Echo", &uniov1.DriveRequest{Drive: want}, &resp); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resp.Drive, want) {
		t.Fatal("echo mismatch")
	}
}

func TestUnknownMethodIsAnError(t *testing.T) {
	n := newTestNet(t)
	n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	var resp uniov1.DriveRequest
	err := b.Call(ctxWithTimeout(t, 5*time.Second), "a", "no.Such", &uniov1.DriveRequest{}, &resp)
	if !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("error = %v, want ErrUnknownMethod", err)
	}
}

func TestHandlerErrorsCrossTheWire(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	handlers := map[string]error{
		"test.Sentinel": drive.ErrShardNotFound,
		"test.S3":       s3err.Errorf(s3err.NoSuchKey, "no such key"),
		"test.Internal": errors.New("disk on fire at /secret/path"),
	}
	for method, herr := range handlers {
		if err := a.Register(method, func(context.Context, ids.NodeID, []byte) ([]byte, error) {
			return nil, herr
		}); err != nil {
			t.Fatal(err)
		}
	}
	var resp uniov1.DriveRequest
	ctx := ctxWithTimeout(t, 5*time.Second)

	err := b.Call(ctx, "a", "test.Sentinel", &uniov1.DriveRequest{}, &resp)
	if !errors.Is(err, drive.ErrShardNotFound) {
		t.Fatalf("sentinel: %v", err)
	}
	err = b.Call(ctx, "a", "test.S3", &uniov1.DriveRequest{}, &resp)
	if s3err.CodeOf(err) != s3err.NoSuchKey || !strings.Contains(err.Error(), "no such key") {
		t.Fatalf("s3 error: %v", err)
	}
	err = b.Call(ctx, "a", "test.Internal", &uniov1.DriveRequest{}, &resp)
	if s3err.CodeOf(err) != s3err.InternalError {
		t.Fatalf("internal error code = %s", s3err.CodeOf(err))
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "fire") {
		t.Fatalf("internal details leaked: %v", err)
	}
}

func TestPanickingHandlerBecomesInternalError(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.Register("test.Boom", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		panic("boom")
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Register("test.Echo", echoHandler); err != nil {
		t.Fatal(err)
	}
	ctx := ctxWithTimeout(t, 5*time.Second)
	var resp uniov1.DriveRequest
	err := b.Call(ctx, "a", "test.Boom", &uniov1.DriveRequest{}, &resp)
	if s3err.CodeOf(err) != s3err.InternalError {
		t.Fatalf("panic error = %v", err)
	}
	// The node keeps serving after a handler panic.
	if err := b.Call(ctx, "a", "test.Echo", &uniov1.DriveRequest{Drive: []byte("ok")}, &resp); err != nil {
		t.Fatalf("call after panic: %v", err)
	}
}

func TestDeadlineReachesHandler(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	seen := make(chan bool, 1)
	if err := a.Register("test.Deadline", func(ctx context.Context, _ ids.NodeID, _ []byte) ([]byte, error) {
		_, ok := ctx.Deadline()
		seen <- ok
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	var resp uniov1.DriveRequest
	if err := b.Call(ctxWithTimeout(t, 5*time.Second), "a", "test.Deadline", &uniov1.DriveRequest{}, &resp); err != nil {
		t.Fatal(err)
	}
	if !<-seen {
		t.Fatal("handler context has no deadline")
	}
}

func TestCancelStopsHandler(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	entered := make(chan struct{})
	observed := make(chan error, 1)
	if err := a.Register("test.Block", func(ctx context.Context, _ ids.NodeID, _ []byte) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		observed <- ctx.Err()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callErr := make(chan error, 1)
	go func() {
		var resp uniov1.DriveRequest
		callErr <- b.Call(ctx, "a", "test.Block", &uniov1.DriveRequest{}, &resp)
	}()
	<-entered
	cancel()
	if err := <-callErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller error = %v, want context.Canceled", err)
	}
	if err := <-observed; !errors.Is(err, context.Canceled) {
		t.Fatalf("handler saw %v, want cancellation", err)
	}
}

func TestDeadlineExpiresCall(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	if err := a.Register("test.Hang", func(ctx context.Context, _ ids.NodeID, _ []byte) ([]byte, error) {
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var resp uniov1.DriveRequest
	err := b.Call(ctx, "a", "test.Hang", &uniov1.DriveRequest{}, &resp)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want DeadlineExceeded", err)
	}
}

func TestConcurrentCallsShareConnections(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.Register("test.Echo", echoHandler); err != nil {
		t.Fatal(err)
	}
	const calls = 300
	ctx := ctxWithTimeout(t, 10*time.Second)
	var wg sync.WaitGroup
	errs := make(chan error, calls)
	for i := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := []byte(fmt.Sprintf("payload-%d", i))
			var resp uniov1.DriveRequest
			if err := b.Call(ctx, "a", "test.Echo", &uniov1.DriveRequest{Drive: want}, &resp); err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(resp.Drive, want) {
				errs <- fmt.Errorf("call %d got %q", i, resp.Drive)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if conns := poolSize(b, "a"); conns > b.lim.MaxRPCConnsPerPeer {
		t.Fatalf("pool holds %d connections, limit %d", conns, b.lim.MaxRPCConnsPerPeer)
	}
}

// poolSize counts live RPC connections that t holds to node id.
func poolSize(t *Transport, id ids.NodeID) int {
	p := t.peerFor(id)
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.rpc)
}

func TestHandlerConcurrencyIsBounded(t *testing.T) {
	const limit = 3
	n := newTestNet(t)
	a := n.node("a", Limits{MaxHandlersPerConn: limit})
	b := n.dialOnly("b", Limits{MaxInflightPerConn: 64, MaxRPCConnsPerPeer: 1})
	var running, peak atomic.Int64
	release := make(chan struct{})
	entered := make(chan struct{}, 64)
	if err := a.Register("test.Gate", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		cur := running.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		running.Add(-1)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	const calls = 12
	var wg sync.WaitGroup
	for range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var resp uniov1.DriveRequest
			_ = b.Call(ctxWithTimeout(t, 10*time.Second), "a", "test.Gate", &uniov1.DriveRequest{}, &resp)
		}()
	}
	for range limit {
		<-entered
	}
	// With the limit reached, no further handler may start. Handlers are
	// released one at a time, which lets the queue drain.
	for range calls - limit {
		release <- struct{}{}
		<-entered
	}
	close(release)
	wg.Wait()
	if got := peak.Load(); got > limit {
		t.Fatalf("peak concurrency %d exceeds limit %d", got, limit)
	}
}

func TestRequestOverFrameLimitFailsWithoutDamage(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{MaxFrameSize: 4096})
	b := n.dialOnly("b", Limits{MaxFrameSize: 4096})
	if err := a.Register("test.Echo", echoHandler); err != nil {
		t.Fatal(err)
	}
	ctx := ctxWithTimeout(t, 5*time.Second)
	var resp uniov1.DriveRequest
	if err := b.Call(ctx, "a", "test.Echo", &uniov1.DriveRequest{Drive: []byte("warm")}, &resp); err != nil {
		t.Fatal(err)
	}
	err := b.Call(ctx, "a", "test.Echo", &uniov1.DriveRequest{Drive: make([]byte, 8192)}, &resp)
	if err == nil {
		t.Fatal("oversize request accepted")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("oversize request damaged the connection: %v", err)
	}
	if err := b.Call(ctx, "a", "test.Echo", &uniov1.DriveRequest{Drive: []byte("again")}, &resp); err != nil {
		t.Fatalf("connection unusable after oversize request: %v", err)
	}
}

func TestResponseOverFrameLimitBecomesInternalError(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{MaxFrameSize: 4096})
	b := n.dialOnly("b", Limits{MaxFrameSize: 4096})
	if err := a.Register("test.Big", func(context.Context, ids.NodeID, []byte) ([]byte, error) {
		return make([]byte, 8192), nil
	}); err != nil {
		t.Fatal(err)
	}
	var resp uniov1.DriveRequest
	err := b.Call(ctxWithTimeout(t, 5*time.Second), "a", "test.Big", &uniov1.DriveRequest{}, &resp)
	if s3err.CodeOf(err) != s3err.InternalError {
		t.Fatalf("error = %v, want InternalError", err)
	}
}

func TestUnreachablePeerIsUnavailable(t *testing.T) {
	n := newTestNet(t)
	b := n.dialOnly("b", Limits{})
	n.setAddr("ghost", "127.0.0.1:1")
	var resp uniov1.DriveRequest
	err := b.Call(ctxWithTimeout(t, 5*time.Second), "ghost", "test.Echo", &uniov1.DriveRequest{}, &resp)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if s3err.CodeOf(err) != s3err.ServiceUnavailable {
		t.Fatalf("code = %s", s3err.CodeOf(err))
	}
}

func TestPeerIdentityMismatchRejected(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	n.setAddr("c", n.addrOf(a))
	var resp uniov1.DriveRequest
	err := b.Call(ctxWithTimeout(t, 5*time.Second), "c", "test.Echo", &uniov1.DriveRequest{}, &resp)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("call to a node answering as another identity: %v", err)
	}
}

// addrOf returns the listen address of tr in the test network.
func (n *testNet) addrOf(tr *Transport) string {
	n.t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.ln.Addr().String()
}

func TestCloseFailsCallsWithoutHanging(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	entered := make(chan struct{})
	if err := a.Register("test.Block", func(ctx context.Context, _ ids.NodeID, _ []byte) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		var resp uniov1.DriveRequest
		done <- b.Call(context.Background(), "a", "test.Block", &uniov1.DriveRequest{}, &resp)
	}()
	<-entered
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("call succeeded after Close")
	}
	var resp uniov1.DriveRequest
	if err := b.Call(context.Background(), "a", "test.Echo", &uniov1.DriveRequest{}, &resp); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after Close = %v, want ErrClosed", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	cases := []struct {
		name    string
		method  string
		handler cluster.Handler
	}{
		{name: "empty name", method: "", handler: echoHandler},
		{name: "name too long", method: strings.Repeat("m", maxMethodLen+1), handler: echoHandler},
		{name: "nil handler", method: "test.Nil", handler: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := a.Register(tc.method, tc.handler); err == nil {
				t.Fatal("invalid registration accepted")
			}
		})
	}
	if err := a.Register("test.Dup", echoHandler); err != nil {
		t.Fatal(err)
	}
	if err := a.Register("test.Dup", echoHandler); err == nil {
		t.Fatal("duplicate registration accepted")
	}
}

func TestIdleConnectionsAreReaped(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{IdleTimeout: 200 * time.Millisecond, ReadTimeout: time.Second, PingInterval: 100 * time.Millisecond})
	if err := a.Register("test.Echo", echoHandler); err != nil {
		t.Fatal(err)
	}
	var resp uniov1.DriveRequest
	if err := b.Call(ctxWithTimeout(t, 5*time.Second), "a", "test.Echo", &uniov1.DriveRequest{}, &resp); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for poolSize(b, "a") != 0 {
		select {
		case <-deadline:
			t.Fatal("idle connection was not reaped")
		case <-time.After(20 * time.Millisecond):
		}
	}
	// A later call dials a fresh connection and succeeds.
	if err := b.Call(ctxWithTimeout(t, 5*time.Second), "a", "test.Echo", &uniov1.DriveRequest{Drive: []byte("x")}, &resp); err != nil {
		t.Fatalf("call after reap: %v", err)
	}
}

func TestKeepalivePreventsReadTimeout(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	lim := Limits{PingInterval: 40 * time.Millisecond, ReadTimeout: 200 * time.Millisecond, IdleTimeout: 10 * time.Second}
	b := n.dialOnly("b", lim)
	if err := a.Register("test.Echo", echoHandler); err != nil {
		t.Fatal(err)
	}
	var resp uniov1.DriveRequest
	ctx := ctxWithTimeout(t, 5*time.Second)
	if err := b.Call(ctx, "a", "test.Echo", &uniov1.DriveRequest{}, &resp); err != nil {
		t.Fatal(err)
	}
	before := poolSize(b, "a")
	// Stay idle for several read timeouts; pings must keep the connection up.
	<-time.After(3 * lim.ReadTimeout)
	if got := poolSize(b, "a"); got != before || got == 0 {
		t.Fatalf("connection lost while idle: pool %d -> %d", before, got)
	}
	if err := b.Call(ctx, "a", "test.Echo", &uniov1.DriveRequest{Drive: []byte("y")}, &resp); err != nil {
		t.Fatalf("call after idle: %v", err)
	}
}

package uniwire

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/s3err"
)

// echoStream replies with the uploaded bytes, the request payload and the
// SHA-256 of the upload in the reply payload.
func echoStream(_ context.Context, req *StreamRequest) (*StreamResponse, error) {
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	payload := append(append([]byte{}, req.Payload...), sum[:]...)
	return &StreamResponse{
		Payload: payload,
		Body:    io.NopCloser(bytes.NewReader(data)),
		BodyLen: int64(len(data)),
	}, nil
}

// readAll reads the whole reply body and closes it. It is safe to call from
// any goroutine.
func readAll(rep Reply) ([]byte, error) {
	if rep.Body == nil {
		return nil, nil
	}
	defer func() { _ = rep.Body.Close() }()
	return io.ReadAll(rep.Body)
}

// readReply is readAll for the test goroutine.
func readReply(t *testing.T, rep Reply) []byte {
	t.Helper()
	data, err := readAll(rep)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStreamEchoRoundTrip(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 1<<20)
	_, _ = rand.Read(data)
	rep, err := b.Stream(ctxWithTimeout(t, 10*time.Second), "a", "test.Echo", []byte("meta"), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(rep.Payload, []byte("meta")) {
		t.Fatalf("payload = %q", rep.Payload)
	}
	sum := sha256.Sum256(data)
	if !bytes.HasSuffix(rep.Payload, sum[:]) {
		t.Fatal("server hashed different bytes than were sent")
	}
	if got := readReply(t, rep); !bytes.Equal(got, data) {
		t.Fatal("reply body differs from upload")
	}
}

func TestStreamLargeTransferIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("large transfer")
	}
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 32<<20)
	_, _ = rand.Read(data)
	rep, err := b.Stream(ctxWithTimeout(t, 60*time.Second), "a", "test.Echo", nil, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got := readReply(t, rep); !bytes.Equal(got, data) {
		t.Fatal("32 MiB round trip corrupted data")
	}
}

func TestStreamEmptyTransfer(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	rep, err := b.Stream(ctxWithTimeout(t, 5*time.Second), "a", "test.Echo", []byte("none"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Body != nil || rep.BodyLen != 0 {
		t.Fatalf("empty reply has body %v, len %d", rep.Body, rep.BodyLen)
	}
}

func TestStreamErrorTrailerKeepsConnection(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{MaxStreamConnsPerPeer: 1})
	if err := a.RegisterStream("test.Exists", func(ctx context.Context, req *StreamRequest) (*StreamResponse, error) {
		return nil, drive.ErrShardExists
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	ctx := ctxWithTimeout(t, 10*time.Second)
	body := make([]byte, 4096)
	_, err := b.Stream(ctx, "a", "test.Exists", nil, bytes.NewReader(body), int64(len(body)))
	if !errors.Is(err, drive.ErrShardExists) {
		t.Fatalf("error = %v, want ErrShardExists", err)
	}
	// The unread upload was drained, so the same connection still works.
	rep, err := b.Stream(ctx, "a", "test.Echo", nil, bytes.NewReader([]byte("ok")), 2)
	if err != nil {
		t.Fatalf("call after error trailer: %v", err)
	}
	if got := readReply(t, rep); string(got) != "ok" {
		t.Fatalf("reply = %q", got)
	}
}

func TestStreamHandlerIgnoringBodyStaysUsable(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{MaxStreamConnsPerPeer: 1})
	if err := a.RegisterStream("test.Skip", func(context.Context, *StreamRequest) (*StreamResponse, error) {
		return &StreamResponse{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	ctx := ctxWithTimeout(t, 10*time.Second)
	body := bytes.Repeat([]byte{7}, 1<<20)
	if _, err := b.Stream(ctx, "a", "test.Skip", nil, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	rep, err := b.Stream(ctx, "a", "test.Echo", nil, bytes.NewReader([]byte("again")), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := readReply(t, rep); string(got) != "again" {
		t.Fatalf("reply = %q", got)
	}
}

func TestStreamUnknownMethod(t *testing.T) {
	n := newTestNet(t)
	n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	_, err := b.Stream(ctxWithTimeout(t, 5*time.Second), "a", "no.Such", nil, bytes.NewReader([]byte("x")), 1)
	if !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("error = %v, want ErrUnknownMethod", err)
	}
}

func TestStreamSourceErrorIsReturnedAsIs(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	local := errors.New("local disk read failed")
	src := io.MultiReader(bytes.NewReader(make([]byte, 100)), &failingReader{err: local})
	_, err := b.Stream(ctxWithTimeout(t, 5*time.Second), "a", "test.Echo", nil, src, 1000)
	if !errors.Is(err, local) {
		t.Fatalf("error = %v, want the source error", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatal("local source failure reported as peer unavailable")
	}
}

// failingReader returns its bytes and then err.
type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

func TestStreamShortBodyFails(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	_, err := b.Stream(ctxWithTimeout(t, 5*time.Second), "a", "test.Echo", nil, bytes.NewReader(make([]byte, 10)), 100)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestStreamReplyClosedEarlyDoesNotReuse(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{MaxStreamConnsPerPeer: 1})
	if err := a.RegisterStream("test.Big", func(context.Context, *StreamRequest) (*StreamResponse, error) {
		data := bytes.Repeat([]byte{1}, 1<<20)
		return &StreamResponse{Body: io.NopCloser(bytes.NewReader(data)), BodyLen: int64(len(data))}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	ctx := ctxWithTimeout(t, 10*time.Second)
	rep, err := b.Stream(ctx, "a", "test.Big", nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10)
	if _, err := io.ReadFull(rep.Body, buf); err != nil {
		t.Fatal(err)
	}
	if err := rep.Body.Close(); err != nil {
		t.Fatal(err)
	}
	// The abandoned transfer must not poison the pool: the next call works.
	rep2, err := b.Stream(ctx, "a", "test.Echo", nil, bytes.NewReader([]byte("fine")), 4)
	if err != nil {
		t.Fatalf("call after early close: %v", err)
	}
	if got := readReply(t, rep2); string(got) != "fine" {
		t.Fatalf("reply = %q", got)
	}
}

func TestStreamConnectionIsReused(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{MaxStreamConnsPerPeer: 4})
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	ctx := ctxWithTimeout(t, 10*time.Second)
	for range 3 {
		rep, err := b.Stream(ctx, "a", "test.Echo", nil, bytes.NewReader([]byte("r")), 1)
		if err != nil {
			t.Fatal(err)
		}
		readReply(t, rep)
	}
	p := b.peerFor("a")
	p.mu.Lock()
	idle := len(p.idle)
	p.mu.Unlock()
	if idle != 1 {
		t.Fatalf("idle stream connections = %d, want 1 reused connection", idle)
	}
}

func TestStreamDeadlineIsPropagated(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	seen := make(chan bool, 1)
	if err := a.RegisterStream("test.Deadline", func(ctx context.Context, _ *StreamRequest) (*StreamResponse, error) {
		_, ok := ctx.Deadline()
		seen <- ok
		return &StreamResponse{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stream(ctxWithTimeout(t, 5*time.Second), "a", "test.Deadline", nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if !<-seen {
		t.Fatal("stream handler has no deadline")
	}
}

func TestStreamCallerDeadlineAbortsTransfer(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	if err := a.RegisterStream("test.Hang", func(ctx context.Context, _ *StreamRequest) (*StreamResponse, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := b.Stream(ctx, "a", "test.Hang", nil, nil, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want DeadlineExceeded", err)
	}
}

func TestStreamHandlerPanicBecomesInternalError(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	if err := a.RegisterStream("test.Boom", func(context.Context, *StreamRequest) (*StreamResponse, error) {
		panic("kaboom")
	}); err != nil {
		t.Fatal(err)
	}
	_, err := b.Stream(ctxWithTimeout(t, 5*time.Second), "a", "test.Boom", nil, nil, 0)
	if s3err.CodeOf(err) != s3err.InternalError {
		t.Fatalf("error = %v, want InternalError", err)
	}
}

func TestStreamConcurrentTransfersAreBounded(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{MaxStreamConnsPerPeer: 2})
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}
	ctx := ctxWithTimeout(t, 30*time.Second)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte(i)}, 64<<10)
			rep, err := b.Stream(ctx, "a", "test.Echo", nil, bytes.NewReader(data), int64(len(data)))
			if err != nil {
				errs <- fmt.Errorf("transfer %d: %w", i, err)
				return
			}
			got, err := readAll(rep)
			if err != nil {
				errs <- fmt.Errorf("transfer %d read: %w", i, err)
				return
			}
			if !bytes.Equal(got, data) {
				errs <- fmt.Errorf("transfer %d corrupted", i)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	p := b.peerFor("a")
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.idle) > 2 {
		t.Fatalf("idle connections %d exceed the bound", len(p.idle))
	}
}

func TestStreamRejectsInvalidArguments(t *testing.T) {
	n := newTestNet(t)
	n.node("a", Limits{})
	b := n.dialOnly("b", Limits{})
	ctx := ctxWithTimeout(t, 5*time.Second)
	cases := []struct {
		name   string
		method string
		body   io.Reader
		length int64
	}{
		{name: "negative length", method: "test.Echo", body: bytes.NewReader(nil), length: -1},
		{name: "length without reader", method: "test.Echo", body: nil, length: 4},
		{name: "empty method", method: "", body: nil, length: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := b.Stream(ctx, "a", tc.method, nil, tc.body, tc.length); err == nil {
				t.Fatal("invalid stream accepted")
			}
		})
	}
}

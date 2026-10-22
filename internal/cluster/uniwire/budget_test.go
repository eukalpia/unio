package uniwire

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
)

// gatedReader serves its data, but its second Read blocks until unblock is
// called. A transfer reads its body into a pooled buffer, so while the read
// is blocked the buffer is charged to the budget.
type gatedReader struct {
	data    []byte
	reads   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

// newGatedReader returns a gatedReader over data.
func newGatedReader(data []byte) *gatedReader {
	return &gatedReader{data: data, started: make(chan struct{}), release: make(chan struct{})}
}

// Read implements io.Reader. Only the goroutine that runs the transfer calls
// it, so reads does not need a lock.
func (g *gatedReader) Read(p []byte) (int, error) {
	g.reads++
	if g.reads == 2 {
		close(g.started)
		<-g.release
	}
	if len(g.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, g.data)
	g.data = g.data[n:]
	return n, nil
}

// unblock lets the blocked read return. It is safe to call more than once.
func (g *gatedReader) unblock() {
	g.once.Do(func() { close(g.release) })
}

// Close implements io.Closer for use as a stream reply body. It has nothing to
// release.
func (g *gatedReader) Close() error { return nil }

// randomBytes returns size random bytes for a transfer body.
func randomBytes(t *testing.T, size int) []byte {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	return data
}

// awaitStarted waits until the gated read is blocked, or fails the test.
func awaitStarted(t *testing.T, g *gatedReader, ctx context.Context) {
	t.Helper()
	select {
	case <-g.started:
	case <-ctx.Done():
		t.Fatal("transfer never reached the blocked read")
	}
}

func TestNewRejectsSharedBudgetSmallerThanABuffer(t *testing.T) {
	small := bufpool.NewBudget(16 << 10)
	_, err := New(Config{Limits: Limits{StreamBufferSize: 32 << 10, StreamBudget: small}})
	if err == nil {
		t.Fatal("New accepted a shared budget smaller than one stream buffer")
	}
}

func TestNewIgnoresPrivateBudgetWhenSharedBudgetIsSet(t *testing.T) {
	shared := bufpool.NewBudget(1 << 20)
	tr, err := New(Config{Limits: Limits{
		StreamBufferSize:   32 << 10,
		StreamBufferBudget: 1, // would be rejected if it were used
		StreamBudget:       shared,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestStreamUploadDrawsFromSharedBudget checks that the client side of a
// stream takes its copy buffer from the shared budget. The server keeps a
// private budget, so only the client's use shows up in the shared one.
func TestStreamUploadDrawsFromSharedBudget(t *testing.T) {
	const bufSize = 32 << 10
	shared := bufpool.NewBudget(1 << 20)
	n := newTestNet(t)
	a := n.node("a", Limits{})
	b := n.dialOnly("b", Limits{StreamBufferSize: bufSize, StreamBudget: shared})
	if err := a.RegisterStream("test.Echo", echoStream); err != nil {
		t.Fatal(err)
	}

	data := randomBytes(t, 64<<10+1000)
	body := newGatedReader(data)
	t.Cleanup(body.unblock)
	ctx := ctxWithTimeout(t, 30*time.Second)

	type result struct {
		rep Reply
		err error
	}
	done := make(chan result, 1)
	go func() {
		rep, err := b.Stream(ctx, "a", "test.Echo", nil, body, int64(len(data)))
		done <- result{rep, err}
	}()

	awaitStarted(t, body, ctx)
	if got := shared.InUse(); got != bufSize {
		t.Fatalf("shared budget InUse = %d during the upload, want %d", got, bufSize)
	}

	body.unblock()
	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if got := readReply(t, res.rep); !bytes.Equal(got, data) {
		t.Fatal("echo differs from the upload")
	}
	if got := shared.InUse(); got != 0 {
		t.Fatalf("shared budget InUse = %d after the upload, want 0", got)
	}
}

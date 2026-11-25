package s3api

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

// drainInterval is how often a draining tracker closes connections that
// became idle since the last pass.
const drainInterval = 50 * time.Millisecond

// connTracker records the connections of the S3 listener and how many
// requests each one is serving, so that a shutdown closes idle keep-alive
// connections at once. Without it, the graceful shutdown waits for every
// client to close its idle connections, which SDK connection pools never do,
// and every stop takes the whole shutdown timeout.
type connTracker struct {
	mu       sync.Mutex
	conns    map[string]*trackedConn
	draining bool
}

// trackedConn is an accepted connection known to its tracker.
type trackedConn struct {
	net.Conn
	t    *connTracker
	key  string
	busy int // requests in progress; guarded by t.mu
	once sync.Once
}

// newConnTracker returns an empty tracker.
func newConnTracker() *connTracker {
	return &connTracker{conns: make(map[string]*trackedConn)}
}

// add starts tracking c. A connection accepted while draining is closed for
// reading at once.
func (t *connTracker) add(c net.Conn) net.Conn {
	tc := &trackedConn{Conn: c, t: t, key: c.RemoteAddr().String()}
	t.mu.Lock()
	t.conns[tc.key] = tc
	draining := t.draining
	t.mu.Unlock()
	if draining {
		_ = c.SetReadDeadline(time.Now())
	}
	return tc
}

// Close stops tracking the connection and closes it.
func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.t.mu.Lock()
		if c.t.conns[c.key] == c {
			delete(c.t.conns, c.key)
		}
		c.t.mu.Unlock()
	})
	return c.Conn.Close()
}

// begin marks a request on the connection with remote address key as in
// progress, and reports whether the server is draining.
func (t *connTracker) begin(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.conns[key]; c != nil {
		c.busy++
	}
	return t.draining
}

// end marks a request on the connection with remote address key as done, and
// reports whether the server is draining.
func (t *connTracker) end(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.conns[key]; c != nil && c.busy > 0 {
		c.busy--
	}
	return t.draining
}

// closeIdle makes every connection that serves no request fail its next
// read, so the server closes it instead of waiting for another request.
func (t *connTracker) closeIdle() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.conns {
		if c.busy == 0 {
			_ = c.SetReadDeadline(time.Now())
		}
	}
}

// drain closes idle connections until ctx is done. It repeats, because a
// connection becomes idle when its last request ends, and the server resets
// the read deadline when it starts waiting for the next request.
func (t *connTracker) drain(ctx context.Context) {
	t.mu.Lock()
	t.draining = true
	t.mu.Unlock()
	tick := time.NewTicker(drainInterval)
	defer tick.Stop()
	for {
		t.closeIdle()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// trackRequests is the outermost handler: it marks the request's connection
// busy while the request is served, and during a drain asks the client to
// close the connection after the response, so no new request starts on it.
func (t *connTracker) trackRequests(ctx context.Context, c *app.RequestContext) {
	key := c.RemoteAddr().String()
	if t.begin(key) {
		c.Response.Header.SetConnectionClose(true)
	}
	c.Next(ctx)
	if t.end(key) {
		c.Response.Header.SetConnectionClose(true)
	}
}

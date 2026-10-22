package uniwire

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// rpcReply is the outcome of one call, delivered to its waiting goroutine.
type rpcReply struct {
	payload []byte
	err     error
}

// rpcConn is a client-side RPC connection. Many calls share it: each gets a
// request id, and a reader goroutine routes replies to the waiting call.
// A rpcConn is dead once any I/O or protocol error occurs; it is then closed
// and removed from its peer.
type rpcConn struct {
	t        *Transport
	p        *peer
	nc       net.Conn
	w        frameWriter
	slots    chan struct{} // bounds calls in flight
	nextID   atomic.Uint64
	lastUsed atomic.Int64 // unix nanos of the last call finished

	// pmu guards pending, dead and cause. closedCh is closed under pmu when
	// the connection dies.
	pmu      sync.Mutex
	pending  map[uint64]chan rpcReply
	dead     bool
	cause    error
	closedCh chan struct{}
}

// newRPCConn wraps an established connection and starts its reader and
// keepalive goroutines.
func (t *Transport) newRPCConn(p *peer, nc net.Conn) *rpcConn {
	c := &rpcConn{
		t:        t,
		p:        p,
		nc:       nc,
		w:        frameWriter{nc: nc, max: t.lim.MaxFrameSize, timeout: t.lim.WriteTimeout},
		slots:    make(chan struct{}, t.lim.MaxInflightPerConn),
		pending:  map[uint64]chan rpcReply{},
		closedCh: make(chan struct{}),
	}
	c.lastUsed.Store(time.Now().UnixNano())
	if !t.goTracked(c.readLoop) || !t.goTracked(c.pingLoop) {
		c.fail(ErrClosed)
	}
	return c
}

// isDead reports whether the connection has failed.
func (c *rpcConn) isDead() bool {
	select {
	case <-c.closedCh:
		return true
	default:
		return false
	}
}

// lastUsedBefore reports whether the connection finished its last call
// before cutoff.
func (c *rpcConn) lastUsedBefore(cutoff time.Time) bool {
	return time.Unix(0, c.lastUsed.Load()).Before(cutoff)
}

// fail marks the connection dead with cause, fails every pending call, closes
// the socket and removes the connection from its peer. Only the first call
// has an effect.
func (c *rpcConn) fail(cause error) {
	c.pmu.Lock()
	if c.dead {
		c.pmu.Unlock()
		return
	}
	c.dead = true
	c.cause = cause
	c.pending = nil
	close(c.closedCh)
	c.pmu.Unlock()

	_ = c.nc.Close()
	c.t.untrackConn(c.nc)
	c.p.forgetRPC(c)
}

// deadErr is the error for a call that met a dead connection.
func (c *rpcConn) deadErr() error {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	return c.deadErrLocked()
}

// deadErrLocked is deadErr for callers that already hold pmu.
func (c *rpcConn) deadErrLocked() error {
	cause := c.cause
	if cause == nil {
		cause = errors.New("connection closed")
	}
	return fmt.Errorf("uniwire: peer %s: %w: %w", c.p.id, ErrUnavailable, cause)
}

// register records a waiter for id. It fails when the connection is dead.
func (c *rpcConn) register(id uint64, ch chan rpcReply) error {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	if c.dead {
		return c.deadErrLocked()
	}
	c.pending[id] = ch
	return nil
}

// unregister removes the waiter for id, if it is still present.
func (c *rpcConn) unregister(id uint64) {
	c.pmu.Lock()
	delete(c.pending, id)
	c.pmu.Unlock()
}

// deliver hands a reply to the waiter for id, if one is still waiting. The
// waiter's channel has capacity one and is written only once, so this never
// blocks.
func (c *rpcConn) deliver(id uint64, r rpcReply) {
	c.pmu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.pmu.Unlock()
	if ok {
		ch <- r
	}
}

// call sends one request and waits for its reply, the context, or the
// connection's death.
func (c *rpcConn) call(ctx context.Context, method string, body []byte) ([]byte, error) {
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closedCh:
		return nil, c.deadErr()
	case <-c.t.done:
		return nil, ErrClosed
	}
	defer func() {
		<-c.slots
		c.lastUsed.Store(time.Now().UnixNano())
	}()

	rem := remainingNanos(ctx)
	if rem < 0 {
		return nil, ctx.Err()
	}
	if rpcFixedLen+len(method)+8+len(body) > c.t.lim.MaxFrameSize {
		return nil, fmt.Errorf("uniwire: request to %s is %d bytes, over the frame limit", method, len(body))
	}
	id := c.nextID.Add(1)
	ch := make(chan rpcReply, 1)
	if err := c.register(id, ch); err != nil {
		return nil, err
	}
	defer c.unregister(id)

	var timeout [8]byte
	binary.BigEndian.PutUint64(timeout[:], uint64(rem))
	if err := c.w.writeRPC(deadlineOf(ctx), frameRequest, id, method, timeout[:], body); err != nil {
		c.fail(err)
		return nil, c.deadErr()
	}

	select {
	case r := <-ch:
		return r.payload, r.err
	case <-ctx.Done():
		c.cancel(id)
		return nil, ctx.Err()
	case <-c.closedCh:
		return nil, c.deadErr()
	case <-c.t.done:
		return nil, ErrClosed
	}
}

// cancel tells the peer to abandon request id. The peer's reply, if any,
// is discarded.
func (c *rpcConn) cancel(id uint64) {
	if err := c.w.writeRPC(time.Time{}, frameCancel, id, ""); err != nil {
		c.fail(err)
	}
}

// readLoop routes replies to their callers until the connection fails. The
// read deadline is refreshed for every frame, so a silent peer is detected
// after ReadTimeout.
func (c *rpcConn) readLoop() {
	for {
		if err := c.nc.SetReadDeadline(time.Now().Add(c.t.lim.ReadTimeout)); err != nil {
			c.fail(err)
			return
		}
		f, err := readRPCFrame(c.nc, c.t.lim.MaxFrameSize)
		if err != nil {
			c.fail(err)
			return
		}
		switch f.typ {
		case frameResponse:
			c.deliver(f.id, rpcReply{payload: f.payload})
		case frameError:
			var e uniov1.RPCError
			if err := e.UnmarshalVT(f.payload); err != nil {
				c.fail(fmt.Errorf("%w: error frame: %w", errMalformedFrame, err))
				return
			}
			c.deliver(f.id, rpcReply{err: decodeError(&e)})
		case framePing:
			if err := c.w.writeRPC(time.Time{}, framePong, 0, ""); err != nil {
				c.fail(err)
				return
			}
		case framePong:
		default:
			c.fail(fmt.Errorf("%w: peer sent frame type %d on a client connection", errMalformedFrame, f.typ))
			return
		}
	}
}

// pingLoop sends a keepalive ping every PingInterval until the connection
// dies or the transport closes.
func (c *rpcConn) pingLoop() {
	tk := time.NewTicker(c.t.lim.PingInterval)
	defer tk.Stop()
	for {
		select {
		case <-c.closedCh:
			return
		case <-c.t.done:
			return
		case <-tk.C:
			if err := c.w.writeRPC(time.Time{}, framePing, 0, ""); err != nil {
				c.fail(err)
				return
			}
		}
	}
}

// splitRequest separates a request payload into its deadline and body. The
// deadline is the caller's remaining time in nanoseconds; zero means none.
//
// The peer's value is untrusted. A value above the largest int64 is clamped to
// it rather than wrapped into a negative deadline. The server caps every
// deadline at StreamTimeout anyway, so the clamp never grants more time than the
// server allows.
func splitRequest(payload []byte) (timeout int64, body []byte, err error) {
	if len(payload) < 8 {
		return 0, nil, fmt.Errorf("%w: request without deadline prefix", errMalformedFrame)
	}
	raw := binary.BigEndian.Uint64(payload[:8])
	if raw > math.MaxInt64 {
		raw = math.MaxInt64
	}
	return int64(raw), payload[8:], nil
}

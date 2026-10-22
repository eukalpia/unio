package uniwire

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/pki"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// acceptLoop accepts peers until the listener closes. Connections beyond
// MaxInboundConns are refused immediately, before any TLS work.
func (t *Transport) acceptLoop(ln net.Listener) {
	for {
		raw, err := ln.Accept()
		if err != nil {
			if t.isClosed() {
				return
			}
			t.log.Warn("uniwire: accept failed", "err", err)
			select {
			case <-time.After(100 * time.Millisecond):
			case <-t.done:
				return
			}
			continue
		}
		if !t.inbound.TryAcquire(1) {
			t.log.Warn("uniwire: inbound connection limit reached, refusing peer", "remote", raw.RemoteAddr().String())
			_ = raw.Close()
			continue
		}
		if !t.trackConn(raw) {
			_ = raw.Close()
			t.inbound.Release(1)
			return
		}
		ok := t.goTracked(func() {
			defer t.inbound.Release(1)
			t.serveConn(raw)
		})
		if !ok {
			t.untrackConn(raw)
			_ = raw.Close()
			t.inbound.Release(1)
			return
		}
	}
}

// serveConn completes the TLS handshake and the preamble of an inbound
// connection, then serves it as RPC or as stream.
func (t *Transport) serveConn(raw net.Conn) {
	defer t.untrackConn(raw)
	defer func() { _ = raw.Close() }()

	tc := tls.Server(raw, t.server)
	hctx, cancel := context.WithTimeout(context.Background(), t.lim.HandshakeTimeout)
	err := tc.HandshakeContext(hctx)
	cancel()
	if err != nil {
		t.log.Debug("uniwire: handshake failed", "remote", raw.RemoteAddr().String(), "err", err)
		return
	}
	from, err := pki.PeerID(tc.ConnectionState())
	if err != nil {
		t.log.Debug("uniwire: peer without node identity", "remote", raw.RemoteAddr().String(), "err", err)
		return
	}
	if err := tc.SetReadDeadline(time.Now().Add(t.lim.HandshakeTimeout)); err != nil {
		return
	}
	kind, err := readPreamble(tc)
	if err != nil {
		t.log.Debug("uniwire: bad preamble", "from", from, "err", err)
		return
	}
	if err := tc.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	switch kind {
	case kindRPC:
		t.serveRPC(tc, from)
	case kindStream:
		t.serveStream(tc, from)
	}
}

// rpcServerConn is the state of one inbound RPC connection. Its reader
// dispatches requests to handler goroutines, at most MaxHandlersPerConn at a
// time, and cancel frames reach the matching handler through inflight.
type rpcServerConn struct {
	t     *Transport
	nc    net.Conn
	from  ids.NodeID
	w     frameWriter
	ctx   context.Context
	slots chan struct{}

	// imu guards inflight, which maps request ids to their cancel functions.
	imu      sync.Mutex
	inflight map[uint64]context.CancelFunc
	hwg      sync.WaitGroup
}

// serveRPC serves one inbound RPC connection. It returns once the connection
// has failed and every handler it started has returned.
func (t *Transport) serveRPC(nc net.Conn, from ids.NodeID) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &rpcServerConn{
		t:        t,
		nc:       nc,
		from:     from,
		w:        frameWriter{nc: nc, max: t.lim.MaxFrameSize, timeout: t.lim.WriteTimeout},
		ctx:      ctx,
		slots:    make(chan struct{}, t.lim.MaxHandlersPerConn),
		inflight: map[uint64]context.CancelFunc{},
	}
	c.readLoop()
	_ = nc.Close()
	cancel()
	c.hwg.Wait()
}

// readLoop reads frames until the connection fails or breaks the protocol.
func (c *rpcServerConn) readLoop() {
	for {
		if err := c.nc.SetReadDeadline(time.Now().Add(c.t.lim.ReadTimeout)); err != nil {
			return
		}
		f, err := readRPCFrame(c.nc, c.t.lim.MaxFrameSize)
		if err != nil {
			return
		}
		switch f.typ {
		case frameRequest:
			if !c.dispatch(f) {
				return
			}
		case frameCancel:
			c.cancelRequest(f.id)
		case framePing:
			if err := c.w.writeRPC(time.Time{}, framePong, 0, ""); err != nil {
				return
			}
		case framePong:
		default:
			// Replies and errors are never sent to a server.
			return
		}
	}
}

// dispatch starts the handler for a request, or answers it at once when the
// method is unknown. It reports false when the connection must close.
func (c *rpcServerConn) dispatch(f rpcFrame) bool {
	timeout, body, err := splitRequest(f.payload)
	if err != nil {
		return false
	}
	h := c.t.rpcHandler(f.method)
	if h == nil {
		return c.w.writeRPC(time.Time{}, frameError, f.id, "", mustMarshal(encodeError(ErrUnknownMethod))) == nil
	}

	select {
	case c.slots <- struct{}{}:
	case <-c.ctx.Done():
		return false
	}
	var hctx context.Context
	var hcancel context.CancelFunc
	if timeout > 0 {
		hctx, hcancel = context.WithTimeout(c.ctx, min(time.Duration(timeout), c.t.lim.StreamTimeout))
	} else {
		hctx, hcancel = context.WithCancel(c.ctx)
	}
	c.imu.Lock()
	if _, dup := c.inflight[f.id]; dup {
		c.imu.Unlock()
		hcancel()
		<-c.slots
		return false
	}
	c.inflight[f.id] = hcancel
	c.imu.Unlock()

	c.hwg.Add(1)
	go func() {
		defer c.hwg.Done()
		defer func() { <-c.slots }()
		c.run(hctx, hcancel, f.id, f.method, h, body)
	}()
	return true
}

// cancelRequest cancels the handler of request id, if it is still running.
func (c *rpcServerConn) cancelRequest(id uint64) {
	c.imu.Lock()
	defer c.imu.Unlock()
	if cancel, ok := c.inflight[id]; ok {
		cancel()
	}
}

// run executes one handler and writes its reply. A response over the frame
// limit is replaced by an InternalError. A write failure closes the
// connection, which unblocks the reader.
func (c *rpcServerConn) run(ctx context.Context, cancel context.CancelFunc, id uint64, method string, h cluster.Handler, body []byte) {
	resp, err := c.t.invoke(ctx, h, method, c.from, body)
	c.imu.Lock()
	delete(c.inflight, id)
	c.imu.Unlock()
	cancel()

	var werr error
	if err != nil {
		werr = c.w.writeRPC(time.Time{}, frameError, id, "", mustMarshal(encodeError(err)))
	} else {
		werr = c.w.writeRPC(time.Time{}, frameResponse, id, "", resp)
		if errors.Is(werr, errFrameTooLarge) {
			werr = c.w.writeRPC(time.Time{}, frameError, id, "", mustMarshal(encodeError(
				s3err.Wrap(s3err.InternalError, errors.New("response exceeds frame limit")))))
		}
	}
	if werr != nil {
		_ = c.nc.Close()
	}
}

// invoke runs a handler, converting a panic into an InternalError so that a
// bug in one handler cannot take down the node. The stack is logged.
func (t *Transport) invoke(ctx context.Context, h cluster.Handler, method string, from ids.NodeID, req []byte) (resp []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			t.log.Error("uniwire: handler panicked", "method", method, "from", from, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			resp, err = nil, s3err.Wrap(s3err.InternalError, fmt.Errorf("handler panic: %v", r))
		}
	}()
	return h(ctx, from, req)
}

// mustMarshal marshals an error envelope. Marshalling a fixed message has no
// failure mode; an empty payload is returned if that ever changes.
func mustMarshal(e *uniov1.RPCError) []byte {
	b, err := e.MarshalVT()
	if err != nil {
		return nil
	}
	return b
}

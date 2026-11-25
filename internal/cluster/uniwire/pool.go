package uniwire

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/eukalpia/unio/internal/ids"
)

// peer holds the client-side connections to one node: a bounded set of
// multiplexed RPC connections and a bounded set of stream connections.
type peer struct {
	id ids.NodeID
	// dialing has capacity one and serializes dials to this peer, so that
	// concurrent callers do not each open a connection.
	dialing chan struct{}
	// streams bounds stream connections checked out or idle.
	streams *semaphore.Weighted

	// mu guards rpc and idle.
	mu   sync.Mutex
	rpc  []*rpcConn
	idle []*streamConn
}

// peerFor returns the peer record for id, creating it on first use.
func (t *Transport) peerFor(id ids.NodeID) *peer {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[id]
	if !ok {
		p = &peer{
			id:      id,
			dialing: make(chan struct{}, 1),
			streams: semaphore.NewWeighted(int64(t.lim.MaxStreamConnsPerPeer)),
		}
		t.peers[id] = p
	}
	return p
}

// acquireRPC returns a live RPC connection to p, dialing a new one when the
// pool has none with a free slot and is below its bound. Connections are
// picked least loaded first.
func (t *Transport) acquireRPC(ctx context.Context, p *peer) (*rpcConn, error) {
	if c := p.leastLoaded(t.lim.MaxRPCConnsPerPeer); c != nil {
		return c, nil
	}
	select {
	case p.dialing <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, ErrClosed
	}
	defer func() { <-p.dialing }()
	// Another caller may have dialed while this one waited for the token.
	if c := p.leastLoaded(t.lim.MaxRPCConnsPerPeer); c != nil {
		return c, nil
	}
	nc, err := t.dial(ctx, p.id, kindRPC)
	if err != nil {
		return nil, err
	}
	c := t.newRPCConn(p, nc)
	p.mu.Lock()
	p.rpc = append(p.rpc, c)
	p.mu.Unlock()
	return c, nil
}

// leastLoaded returns the live RPC connection with the fewest calls in
// flight. It returns nil when the pool should grow instead: it is empty, or
// every connection is busy and the pool has room.
func (p *peer) leastLoaded(maxConns int) *rpcConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	live := p.rpc[:0]
	var best *rpcConn
	for _, c := range p.rpc {
		if c.isDead() {
			continue
		}
		live = append(live, c)
		if best == nil || len(c.slots) < len(best.slots) {
			best = c
		}
	}
	p.rpc = live
	if best == nil {
		return nil
	}
	if len(best.slots) == 0 || len(p.rpc) >= maxConns {
		return best
	}
	return nil
}

// forgetRPC removes c from the pool.
func (p *peer) forgetRPC(c *rpcConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, x := range p.rpc {
		if x == c {
			p.rpc = append(p.rpc[:i], p.rpc[i+1:]...)
			return
		}
	}
}

// acquireStream checks out a stream connection to p, reusing an idle one
// unless fresh is set. It blocks, subject to ctx, while MaxStreamConnsPerPeer
// connections are checked out.
func (t *Transport) acquireStream(ctx context.Context, p *peer, fresh bool) (*streamConn, error) {
	if err := p.streams.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	if !fresh {
		if sc := p.popIdle(t.lim.IdleTimeout / 2); sc != nil {
			sc.reused = true
			return sc, nil
		}
	}
	nc, err := t.dial(ctx, p.id, kindStream)
	if err != nil {
		p.streams.Release(1)
		return nil, err
	}
	return &streamConn{nc: nc, lastUsed: time.Now()}, nil
}

// popIdle returns the most recently used idle stream connection that is
// younger than maxAge, closing any older ones it passes.
func (p *peer) popIdle(maxAge time.Duration) *streamConn {
	for {
		p.mu.Lock()
		n := len(p.idle)
		if n == 0 {
			p.mu.Unlock()
			return nil
		}
		sc := p.idle[n-1]
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		if time.Since(sc.lastUsed) < maxAge {
			return sc
		}
		_ = sc.nc.Close()
	}
}

// finishStream returns a stream connection to its peer. A healthy connection
// goes back to the idle list; anything else is closed. Either way the
// checkout slot is released.
func (t *Transport) finishStream(p *peer, sc *streamConn, healthy bool) {
	if healthy && !t.isClosed() {
		sc.lastUsed = time.Now()
		p.mu.Lock()
		p.idle = append(p.idle, sc)
		p.mu.Unlock()
	} else {
		_ = sc.nc.Close()
		t.untrackConn(sc.nc)
	}
	p.streams.Release(1)
}

// janitor closes idle connections until the transport closes. It runs one
// goroutine per transport.
func (t *Transport) janitor() {
	tk := time.NewTicker(t.lim.IdleTimeout / 4)
	defer tk.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-tk.C:
			t.reapIdle()
		}
	}
}

// reapIdle closes RPC and stream connections unused for half the idle
// timeout. Clients stop keepalives on closed connections, so servers observe
// the close and release their side.
func (t *Transport) reapIdle() {
	t.mu.Lock()
	peers := make([]*peer, 0, len(t.peers))
	for _, p := range t.peers {
		peers = append(peers, p)
	}
	t.mu.Unlock()

	cutoff := time.Now().Add(-t.lim.IdleTimeout / 2)
	for _, p := range peers {
		var stale []net.Conn
		var staleRPC []*rpcConn
		p.mu.Lock()
		keepRPC := p.rpc[:0]
		for _, c := range p.rpc {
			if len(c.slots) == 0 && c.lastUsedBefore(cutoff) {
				staleRPC = append(staleRPC, c)
			} else {
				keepRPC = append(keepRPC, c)
			}
		}
		p.rpc = keepRPC
		keepIdle := p.idle[:0]
		for _, sc := range p.idle {
			// Idle connections hold no checkout slot, so none is released.
			if sc.lastUsed.Before(cutoff) {
				stale = append(stale, sc.nc)
			} else {
				keepIdle = append(keepIdle, sc)
			}
		}
		p.idle = keepIdle
		p.mu.Unlock()

		for _, c := range staleRPC {
			c.fail(errors.New("uniwire: connection idle"))
		}
		for _, nc := range stale {
			_ = nc.Close()
			t.untrackConn(nc)
		}
	}
}

// streamConn is a stream connection checked out from, or idle in, a peer.
// It is owned by one transfer at a time.
type streamConn struct {
	nc       net.Conn
	lastUsed time.Time
	// nextID is the id of the last transfer started on this connection.
	nextID uint64
	// reused reports that the connection was taken from the idle list, which
	// is the only case where a stale-connection retry is safe.
	reused bool
}

package uniwire

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/cluster/pki"
	"github.com/eukalpia/unio/internal/ids"
)

// maxKeptBuffer bounds the write buffer a frameWriter keeps between frames.
const maxKeptBuffer = 64 << 10

// frameWriter serializes RPC frames onto one connection. A frame is written
// with a single Write call. A failed write may leave a partial frame behind,
// so the connection must be closed after any write error.
type frameWriter struct {
	mu      sync.Mutex
	nc      net.Conn
	buf     []byte
	max     int
	timeout time.Duration
}

// writeRPC encodes one frame and writes it. A zero deadline means now plus
// the writer's timeout. Frames over the size limit fail before any byte is
// written, so the connection stays usable.
func (w *frameWriter) writeRPC(deadline time.Time, typ frameType, id uint64, method string, parts ...[]byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	frame, err := appendRPCFrame(w.buf[:0], w.max, typ, id, method, parts...)
	if err != nil {
		return err
	}
	if cap(frame) <= maxKeptBuffer {
		w.buf = frame
	} else {
		w.buf = nil
	}
	if deadline.IsZero() {
		deadline = time.Now().Add(w.timeout)
	}
	if err := w.nc.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err = w.nc.Write(frame)
	return err
}

// dial opens a connection of kind to node to and checks that the peer
// presents the identity of that node. The preamble is sent before the
// connection is returned. The connection is tracked, so Close closes it.
func (t *Transport) dial(ctx context.Context, to ids.NodeID, kind byte) (net.Conn, error) {
	if t.client == nil || t.resolv == nil {
		return nil, errors.New("uniwire: dialing requires a client TLS configuration and a resolver")
	}
	// Each dial names the node it wants. The standard checks then bind the
	// peer's certificate to that name, so a certificate for another node is
	// refused by the handshake itself.
	sni, err := pki.NodeDNSName(to)
	if err != nil {
		return nil, fmt.Errorf("uniwire: dial %q: %w", to, err)
	}
	cfg := t.client.Clone()
	cfg.ServerName = sni
	addr, err := t.resolv(to)
	if err != nil {
		return nil, unavailable(ctx, to, fmt.Errorf("resolve: %w", err))
	}
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, t.lim.DialTimeout)
	raw, err := d.DialContext(dctx, "tcp", addr)
	cancel()
	if err != nil {
		return nil, unavailable(ctx, to, err)
	}
	tc := tls.Client(raw, cfg)
	hctx, hcancel := context.WithTimeout(ctx, t.lim.HandshakeTimeout)
	err = tc.HandshakeContext(hctx)
	hcancel()
	if err != nil {
		_ = raw.Close()
		return nil, unavailable(ctx, to, err)
	}
	got, err := pki.PeerID(tc.ConnectionState())
	if err == nil && got != to {
		err = fmt.Errorf("dialed %s but the peer identifies as %s", to, got)
	}
	if err != nil {
		_ = tc.Close()
		return nil, unavailable(ctx, to, err)
	}
	if err := writePreambleConn(tc, kind, t.lim.HandshakeTimeout); err != nil {
		_ = tc.Close()
		return nil, unavailable(ctx, to, err)
	}
	if !t.trackConn(tc) {
		_ = tc.Close()
		return nil, ErrClosed
	}
	return tc, nil
}

// writePreambleConn writes the preamble to c under a bounded deadline.
func writePreambleConn(c net.Conn, kind byte, timeout time.Duration) error {
	if err := c.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	return writePreamble(c, kind)
}

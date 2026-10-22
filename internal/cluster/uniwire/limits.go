package uniwire

import (
	"fmt"
	"time"
)

// Limits bounds every resource the transport uses: frame sizes, connections,
// concurrency and timeouts. A zero field takes its DefaultLimits value.
type Limits struct {
	// MaxFrameSize is the largest frame body accepted or sent, in bytes.
	// Frames larger than this are rejected before any allocation.
	MaxFrameSize int
	// HandshakeTimeout bounds the TLS handshake and the preamble.
	HandshakeTimeout time.Duration
	// DialTimeout bounds establishing a TCP connection.
	DialTimeout time.Duration
	// WriteTimeout bounds one frame write when the caller sets no deadline.
	WriteTimeout time.Duration
	// PingInterval is how often a client sends a keepalive ping on an idle
	// RPC connection.
	PingInterval time.Duration
	// ReadTimeout closes an RPC connection that receives nothing for this
	// long. It must exceed PingInterval.
	ReadTimeout time.Duration
	// IdleTimeout is how long an unused stream connection is kept by the
	// server. Clients recycle pooled connections after half of it.
	IdleTimeout time.Duration
	// StreamTimeout bounds a stream transfer whose caller set no deadline.
	StreamTimeout time.Duration
	// MaxRPCConnsPerPeer bounds the multiplexed RPC connections a client
	// keeps to one peer.
	MaxRPCConnsPerPeer int
	// MaxInflightPerConn bounds concurrent calls on one RPC connection.
	MaxInflightPerConn int
	// MaxHandlersPerConn bounds concurrent handlers serving one inbound RPC
	// connection.
	MaxHandlersPerConn int
	// MaxStreamConnsPerPeer bounds stream connections a client holds to one
	// peer, busy or idle.
	MaxStreamConnsPerPeer int
	// MaxInboundConns bounds accepted connections, across all peers.
	MaxInboundConns int
	// StreamBufferSize is the size of each copy buffer used by streams.
	StreamBufferSize int
	// StreamBufferBudget is the total memory for stream copy buffers. A
	// transfer waits for a buffer when the budget is exhausted.
	StreamBufferBudget int64
}

// DefaultLimits returns the limits used when a field is left at zero.
func DefaultLimits() Limits {
	return Limits{
		MaxFrameSize:          4 << 20,
		HandshakeTimeout:      5 * time.Second,
		DialTimeout:           5 * time.Second,
		WriteTimeout:          30 * time.Second,
		PingInterval:          10 * time.Second,
		ReadTimeout:           30 * time.Second,
		IdleTimeout:           60 * time.Second,
		StreamTimeout:         10 * time.Minute,
		MaxRPCConnsPerPeer:    4,
		MaxInflightPerConn:    256,
		MaxHandlersPerConn:    64,
		MaxStreamConnsPerPeer: 8,
		MaxInboundConns:       1024,
		StreamBufferSize:      256 << 10,
		StreamBufferBudget:    64 << 20,
	}
}

// withDefaults returns l with every zero field replaced by its default.
func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxFrameSize == 0 {
		l.MaxFrameSize = d.MaxFrameSize
	}
	if l.HandshakeTimeout == 0 {
		l.HandshakeTimeout = d.HandshakeTimeout
	}
	if l.DialTimeout == 0 {
		l.DialTimeout = d.DialTimeout
	}
	if l.WriteTimeout == 0 {
		l.WriteTimeout = d.WriteTimeout
	}
	if l.PingInterval == 0 {
		l.PingInterval = d.PingInterval
	}
	if l.ReadTimeout == 0 {
		l.ReadTimeout = d.ReadTimeout
	}
	if l.IdleTimeout == 0 {
		l.IdleTimeout = d.IdleTimeout
	}
	if l.StreamTimeout == 0 {
		l.StreamTimeout = d.StreamTimeout
	}
	if l.MaxRPCConnsPerPeer == 0 {
		l.MaxRPCConnsPerPeer = d.MaxRPCConnsPerPeer
	}
	if l.MaxInflightPerConn == 0 {
		l.MaxInflightPerConn = d.MaxInflightPerConn
	}
	if l.MaxHandlersPerConn == 0 {
		l.MaxHandlersPerConn = d.MaxHandlersPerConn
	}
	if l.MaxStreamConnsPerPeer == 0 {
		l.MaxStreamConnsPerPeer = d.MaxStreamConnsPerPeer
	}
	if l.MaxInboundConns == 0 {
		l.MaxInboundConns = d.MaxInboundConns
	}
	if l.StreamBufferSize == 0 {
		l.StreamBufferSize = d.StreamBufferSize
	}
	if l.StreamBufferBudget == 0 {
		l.StreamBufferBudget = d.StreamBufferBudget
	}
	return l
}

// validate reports whether l is internally consistent. It expects defaults
// to have been applied.
func (l Limits) validate() error {
	switch {
	case l.MaxFrameSize < minFrameSize || l.MaxFrameSize > 1<<30:
		return fmt.Errorf("uniwire: MaxFrameSize %d out of range [%d, 1 GiB]", l.MaxFrameSize, minFrameSize)
	case l.PingInterval <= 0 || l.ReadTimeout <= l.PingInterval:
		return fmt.Errorf("uniwire: ReadTimeout %v must exceed PingInterval %v", l.ReadTimeout, l.PingInterval)
	case l.MaxRPCConnsPerPeer < 1 || l.MaxInflightPerConn < 1 || l.MaxHandlersPerConn < 1:
		return fmt.Errorf("uniwire: connection and concurrency limits must be at least 1")
	case l.MaxStreamConnsPerPeer < 1 || l.MaxInboundConns < 1:
		return fmt.Errorf("uniwire: stream and inbound connection limits must be at least 1")
	case l.StreamBufferSize <= 0 || int64(l.StreamBufferSize) > l.StreamBufferBudget:
		return fmt.Errorf("uniwire: StreamBufferSize %d must be positive and within the budget %d", l.StreamBufferSize, l.StreamBufferBudget)
	case l.HandshakeTimeout <= 0 || l.DialTimeout <= 0 || l.WriteTimeout <= 0 || l.IdleTimeout <= 0 || l.StreamTimeout <= 0:
		return fmt.Errorf("uniwire: timeouts must be positive")
	}
	return nil
}

// minFrameSize is the smallest MaxFrameSize that can carry an RPC frame
// header and a short method name.
const minFrameSize = 1 << 10

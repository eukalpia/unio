package uniwire

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

// Config configures a Transport.
type Config struct {
	// Server is the listener configuration (pki.ServerConfig). Required by
	// Listen.
	Server *tls.Config
	// Client is the dialing configuration (pki.ClientConfig). Required by
	// Call and Stream.
	Client *tls.Config
	// Resolve maps a node id to its UniWire address, host:port.
	Resolve func(ids.NodeID) (string, error)
	// Limits bounds resources. Zero fields take their defaults.
	Limits Limits
	// Logger receives transport diagnostics. Nil uses slog.Default.
	Logger *slog.Logger
}

// Transport is a UniWire endpoint. It dials peers, accepts them, and serves
// registered RPC and stream handlers. It implements cluster.RPC. Transport
// is safe for concurrent use; all of its goroutines stop on Close.
type Transport struct {
	client *tls.Config
	server *tls.Config
	resolv func(ids.NodeID) (string, error)
	lim    Limits
	log    *slog.Logger

	buffers *bufpool.Pool
	inbound *semaphore.Weighted

	// mu guards the lifecycle state below.
	mu     sync.Mutex
	closed bool
	ln     net.Listener
	conns  map[net.Conn]struct{}
	peers  map[ids.NodeID]*peer
	wg     sync.WaitGroup
	done   chan struct{}

	// hmu guards the handler tables, which are written at startup.
	hmu            sync.RWMutex
	rpcHandlers    map[string]cluster.Handler
	streamHandlers map[string]StreamHandler
}

// New returns a Transport. It starts the idle-connection janitor and does not
// listen until Listen is called.
func New(cfg Config) (*Transport, error) {
	lim := cfg.Limits.withDefaults()
	if err := lim.validate(); err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	budget := lim.StreamBudget
	if budget == nil {
		budget = bufpool.NewBudget(lim.StreamBufferBudget)
	}
	t := &Transport{
		client:         cfg.Client,
		server:         cfg.Server,
		resolv:         cfg.Resolve,
		lim:            lim,
		log:            log,
		buffers:        bufpool.NewPool(lim.StreamBufferSize, budget),
		inbound:        semaphore.NewWeighted(int64(lim.MaxInboundConns)),
		conns:          map[net.Conn]struct{}{},
		peers:          map[ids.NodeID]*peer{},
		done:           make(chan struct{}),
		rpcHandlers:    map[string]cluster.Handler{},
		streamHandlers: map[string]StreamHandler{},
	}
	t.goTracked(t.janitor)
	return t, nil
}

// Listen starts accepting peers on addr (host:port; port 0 picks a free port)
// and returns the bound address. It fails without a server TLS configuration
// and when the transport already listens.
func (t *Transport) Listen(addr string) (net.Addr, error) {
	if t.server == nil {
		return nil, errors.New("uniwire: Listen requires a server TLS configuration")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("uniwire: listen: %w", err)
	}
	t.mu.Lock()
	if t.closed || t.ln != nil {
		t.mu.Unlock()
		_ = ln.Close()
		if t.closed {
			return nil, ErrClosed
		}
		return nil, errors.New("uniwire: already listening")
	}
	t.ln = ln
	t.mu.Unlock()
	if !t.goTracked(func() { t.acceptLoop(ln) }) {
		_ = ln.Close()
		return nil, ErrClosed
	}
	return ln.Addr(), nil
}

// Close stops the listener and the janitor, closes every connection and
// waits for all goroutines to exit. Calls in flight fail with ErrClosed.
// Close is idempotent.
func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	close(t.done)
	ln := t.ln
	conns := make([]net.Conn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	t.wg.Wait()
	return nil
}

// Register installs the handler for an RPC method. Method names are
// "<module>.<Name>". Registering a name twice fails.
func (t *Transport) Register(method string, h cluster.Handler) error {
	if err := checkMethod(method); err != nil {
		return err
	}
	if h == nil {
		return errors.New("uniwire: nil handler")
	}
	t.hmu.Lock()
	defer t.hmu.Unlock()
	if _, dup := t.rpcHandlers[method]; dup {
		return fmt.Errorf("uniwire: method %q already registered", method)
	}
	t.rpcHandlers[method] = h
	return nil
}

// RegisterStream installs the handler for a stream method. Registering a name
// twice fails.
func (t *Transport) RegisterStream(method string, h StreamHandler) error {
	if err := checkMethod(method); err != nil {
		return err
	}
	if h == nil {
		return errors.New("uniwire: nil stream handler")
	}
	t.hmu.Lock()
	defer t.hmu.Unlock()
	if _, dup := t.streamHandlers[method]; dup {
		return fmt.Errorf("uniwire: stream method %q already registered", method)
	}
	t.streamHandlers[method] = h
	return nil
}

func (t *Transport) rpcHandler(method string) cluster.Handler {
	t.hmu.RLock()
	defer t.hmu.RUnlock()
	return t.rpcHandlers[method]
}

func (t *Transport) streamHandler(method string) StreamHandler {
	t.hmu.RLock()
	defer t.hmu.RUnlock()
	return t.streamHandlers[method]
}

// goTracked runs fn on a goroutine that Close waits for. It reports false,
// without starting fn, once the transport is closed.
func (t *Transport) goTracked(fn func()) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		fn()
	}()
	return true
}

// trackConn records c so that Close can close it. It reports false once the
// transport is closed.
func (t *Transport) trackConn(c net.Conn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.conns[c] = struct{}{}
	return true
}

// untrackConn forgets c.
func (t *Transport) untrackConn(c net.Conn) {
	t.mu.Lock()
	delete(t.conns, c)
	t.mu.Unlock()
}

// isClosed reports whether Close has started.
func (t *Transport) isClosed() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

// checkMethod validates an RPC or stream method name.
func checkMethod(method string) error {
	if method == "" || len(method) > maxMethodLen {
		return fmt.Errorf("uniwire: method name must be 1 to %d bytes", maxMethodLen)
	}
	return nil
}

// unavailable classifies a failure on the path to peer: context errors pass
// through, everything else becomes ErrUnavailable with the cause kept for
// logs. A connection deadline is set to the context deadline, so when it fires
// first the context may not report its own expiry yet; a passed deadline is
// reported as DeadlineExceeded in that case.
func unavailable(ctx context.Context, to ids.NodeID, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) {
			return context.DeadlineExceeded
		}
	}
	return fmt.Errorf("uniwire: peer %s: %w: %w", to, ErrUnavailable, err)
}

// deadlineOf returns the time the caller's context expires, or the
// zero time when it has no deadline.
func deadlineOf(ctx context.Context) time.Time {
	d, _ := ctx.Deadline()
	return d
}

// remainingNanos returns the time left until ctx expires, in nanoseconds, for
// the wire. Zero means no deadline. It returns -1 when the deadline has
// already passed.
func remainingNanos(ctx context.Context) int64 {
	d, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	rem := time.Until(d)
	if rem <= 0 {
		return -1
	}
	return rem.Nanoseconds()
}

package uniwire

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/cluster/pki"
	"github.com/eukalpia/unio/internal/ids"
)

var testSecret = []byte("uniwire-test-secret-0123456789abcdef")

// testNet is a set of loopback transports that share one cluster CA and
// resolve each other through a table of listen addresses.
type testNet struct {
	t  testing.TB
	ca *pki.CA

	mu    sync.Mutex
	addrs map[ids.NodeID]string
}

// newTestNet returns an empty network for one test.
func newTestNet(t testing.TB) *testNet {
	t.Helper()
	ca, err := pki.DeriveCA(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	return &testNet{t: t, ca: ca, addrs: map[ids.NodeID]string{}}
}

// resolve implements Config.Resolve over the address table.
func (n *testNet) resolve(id ids.NodeID) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	addr, ok := n.addrs[id]
	if !ok {
		return "", fmt.Errorf("no address for %s", id)
	}
	return addr, nil
}

// node starts a listening transport for id with the given limits. The
// transport is closed when the test ends.
func (n *testNet) node(id ids.NodeID, lim Limits) *Transport {
	n.t.Helper()
	ident, err := n.ca.Identity(id, []string{"127.0.0.1"})
	if err != nil {
		n.t.Fatal(err)
	}
	server, err := pki.ServerConfig(ident)
	if err != nil {
		n.t.Fatal(err)
	}
	client, err := pki.ClientConfig(ident)
	if err != nil {
		n.t.Fatal(err)
	}
	tr, err := New(Config{Server: server, Client: client, Resolve: n.resolve, Limits: lim, Logger: quietLogger()})
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(func() { _ = tr.Close() })
	addr, err := tr.Listen("127.0.0.1:0")
	if err != nil {
		n.t.Fatal(err)
	}
	n.mu.Lock()
	n.addrs[id] = addr.String()
	n.mu.Unlock()
	return tr
}

// dialOnly starts a transport for id that cannot accept connections.
func (n *testNet) dialOnly(id ids.NodeID, lim Limits) *Transport {
	n.t.Helper()
	ident, err := n.ca.Identity(id, nil)
	if err != nil {
		n.t.Fatal(err)
	}
	client, err := pki.ClientConfig(ident)
	if err != nil {
		n.t.Fatal(err)
	}
	tr, err := New(Config{Client: client, Resolve: n.resolve, Limits: lim, Logger: quietLogger()})
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(func() { _ = tr.Close() })
	return tr
}

// setAddr points id at addr, for tests that need a peer at a wrong address.
func (n *testNet) setAddr(id ids.NodeID, addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.addrs[id] = addr
}

// quietLogger discards transport logs, which tests exercising failures would
// otherwise flood.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

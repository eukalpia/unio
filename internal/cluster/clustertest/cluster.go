package clustertest

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/cluster/pki"
	"github.com/eukalpia/unio/internal/cluster/uniwire"
	"github.com/eukalpia/unio/internal/ids"
)

// secret is the cluster secret of every test cluster. Tests are hermetic, so
// a fixed value is fine.
const secret = "clustertest-secret-0123456789abcdef"

// Cluster is a set of UniWire transports on loopback that trust one cluster
// CA. Tests use it to exercise real mutual TLS between nodes. Every transport
// is closed when the test ends.
type Cluster struct {
	// Nodes maps each node id to its transport. Every node listens.
	Nodes map[ids.NodeID]*uniwire.Transport

	mu    sync.Mutex
	addrs map[ids.NodeID]string
}

// NewCluster starts a listening transport for each node in nodes.
func NewCluster(tb testing.TB, nodes ...ids.NodeID) *Cluster {
	tb.Helper()
	c := &Cluster{
		Nodes: map[ids.NodeID]*uniwire.Transport{},
		addrs: map[ids.NodeID]string{},
	}
	ca, err := pki.DeriveCA([]byte(secret))
	if err != nil {
		tb.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, id := range nodes {
		ident, err := ca.Identity(id, []string{"127.0.0.1"})
		if err != nil {
			tb.Fatal(err)
		}
		server, err := pki.ServerConfig(ident)
		if err != nil {
			tb.Fatal(err)
		}
		client, err := pki.ClientConfig(ident)
		if err != nil {
			tb.Fatal(err)
		}
		tr, err := uniwire.New(uniwire.Config{
			Server:  server,
			Client:  client,
			Resolve: c.resolve,
			Logger:  quiet,
		})
		if err != nil {
			tb.Fatal(err)
		}
		tb.Cleanup(func() { _ = tr.Close() })
		addr, err := tr.Listen("127.0.0.1:0")
		if err != nil {
			tb.Fatal(err)
		}
		c.mu.Lock()
		c.addrs[id] = addr.String()
		c.mu.Unlock()
		c.Nodes[id] = tr
	}
	return c
}

// resolve maps a node id to its listen address.
func (c *Cluster) resolve(id ids.NodeID) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	addr, ok := c.addrs[id]
	if !ok {
		return "", errUnknownNode(id)
	}
	return addr, nil
}

// errUnknownNode reports a lookup of a node that is not in the cluster.
type errUnknownNode ids.NodeID

// Error implements error.
func (e errUnknownNode) Error() string { return "clustertest: no such node " + string(e) }

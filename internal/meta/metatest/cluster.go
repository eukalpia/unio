package metatest

import (
	"fmt"
	"sync"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

// Cluster is an in-process cluster of nodes for tests of the Raft-backed
// store. Nodes talk through the RPC and RaftNetwork views returned by RPC and
// RaftNetwork; Kill and Restart simulate crashes and restarts. All methods
// are safe for concurrent use. The cluster starts no goroutines.
//
// A Cluster holds the handlers and transports of its nodes. Handlers are
// Register-ed per node and are kept across Kill and Restart. Raft transports
// are created per (node, group) and are replaced by Restart.
type Cluster struct {
	mu    sync.Mutex
	nodes map[ids.NodeID]*clusterNode
}

// clusterNode is the state of one node. up is false while the node is killed.
type clusterNode struct {
	id         ids.NodeID
	up         bool
	handlers   map[string]cluster.Handler
	transports map[ids.PartitionID]*raft.InmemTransport
}

// NewCluster returns a cluster of n nodes named "n1" to "nN", all up. For
// n <= 0 the cluster is empty.
func NewCluster(n int) *Cluster {
	nodes := make([]ids.NodeID, 0, max(n, 0))
	for i := 1; i <= n; i++ {
		nodes = append(nodes, ids.NodeID(fmt.Sprintf("n%d", i)))
	}
	return NewClusterWithNodes(nodes...)
}

// NewClusterWithNodes returns a cluster of the given nodes, all up. Duplicate
// ids are ignored.
func NewClusterWithNodes(nodes ...ids.NodeID) *Cluster {
	c := &Cluster{nodes: make(map[ids.NodeID]*clusterNode, len(nodes))}
	for _, id := range nodes {
		if _, dup := c.nodes[id]; dup {
			continue
		}
		c.nodes[id] = &clusterNode{
			id:         id,
			up:         true,
			handlers:   make(map[string]cluster.Handler),
			transports: make(map[ids.PartitionID]*raft.InmemTransport),
		}
	}
	return c
}

// IsUp reports whether node is up. Unknown nodes are not up.
func (c *Cluster) IsUp(node ids.NodeID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	nd, ok := c.nodes[node]
	return ok && nd.up
}

// Kill takes node down. Calls to node over RPC fail, and calls made by node
// fail. Every raft transport of node is disconnected from all peers, and every
// peer's transport to node is disconnected, so AppendEntries, votes and other
// raft RPCs to or from node fail.
//
// Kill does not stop anything that runs on the node. Raft instances built on
// the node's transports keep running until the caller shuts them down, and
// the caller must do so. Registered handlers and the node's transports are
// kept, but the transports stay disconnected until Restart. Killing a node
// that is already down does nothing. An unknown node is an error.
func (c *Cluster) Kill(node ids.NodeID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	nd, err := c.lookupLocked(node)
	if err != nil {
		return err
	}
	c.killLocked(nd)
	return nil
}

// Restart brings node back up. If node is up, it is killed first. Then every
// raft transport the node had is replaced by a new transport, connected to
// every up peer's transport for the same group, and each peer is reconnected
// to the new transport. Groups the node never created get no transport. The
// RPC handlers registered before the kill are still in place.
//
// Raft instances from before the kill are not restarted. The caller creates
// new ones on the transports that Transport returns after Restart. An unknown
// node is an error.
func (c *Cluster) Restart(node ids.NodeID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	nd, err := c.lookupLocked(node)
	if err != nil {
		return err
	}
	c.killLocked(nd)
	nd.up = true
	for group := range nd.transports {
		nd.transports[group] = c.newTransportLocked(nd, group)
	}
	return nil
}

// lookupLocked returns the node with the given id, or an error. c.mu must be
// held.
func (c *Cluster) lookupLocked(node ids.NodeID) (*clusterNode, error) {
	nd, ok := c.nodes[node]
	if !ok {
		return nil, fmt.Errorf("metatest: unknown node %q", node)
	}
	return nd, nil
}

// killLocked marks nd down and severs its raft connections in both
// directions. c.mu must be held.
func (c *Cluster) killLocked(nd *clusterNode) {
	if !nd.up {
		return
	}
	nd.up = false
	for group, t := range nd.transports {
		t.DisconnectAll()
		for _, peer := range c.nodes {
			if peer == nd {
				continue
			}
			if pt := peer.transports[group]; pt != nil {
				pt.Disconnect(raft.ServerAddress(nd.id))
			}
		}
	}
}

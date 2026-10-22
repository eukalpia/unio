package metatest

import (
	"fmt"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

// nodeRaft is the cluster.RaftNetwork of one node.
type nodeRaft struct {
	c    *Cluster
	self ids.NodeID
}

var _ cluster.RaftNetwork = (*nodeRaft)(nil)

// RaftNetwork returns the cluster.RaftNetwork of node.
func (c *Cluster) RaftNetwork(node ids.NodeID) cluster.RaftNetwork {
	return &nodeRaft{c: c, self: node}
}

// Transport returns the raft transport of this node for group. The transport
// is an in-memory raft.InmemTransport whose local address is the node id. It
// is created on first use and then returned unchanged until Restart replaces
// it. A new transport is connected in both directions to the transports of
// every up peer for the same group. A down node has no transports, so the
// call fails for a node that is down.
func (n *nodeRaft) Transport(group ids.PartitionID) (raft.Transport, error) {
	return n.c.transport(n.self, group)
}

// transport returns node's transport for group, creating it if needed. It
// fails for an unknown or down node.
func (c *Cluster) transport(node ids.NodeID, group ids.PartitionID) (raft.Transport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	nd, err := c.lookupLocked(node)
	if err != nil {
		return nil, err
	}
	if !nd.up {
		return nil, fmt.Errorf("metatest: node %s is down", node)
	}
	if t, ok := nd.transports[group]; ok {
		return t, nil
	}
	t := c.newTransportLocked(nd, group)
	nd.transports[group] = t
	return t, nil
}

// newTransportLocked returns a new transport for nd and group, connected in
// both directions to every up peer that has a transport for group. c.mu must
// be held.
func (c *Cluster) newTransportLocked(nd *clusterNode, group ids.PartitionID) *raft.InmemTransport {
	_, t := raft.NewInmemTransport(raft.ServerAddress(nd.id))
	for _, peer := range c.nodes {
		if peer == nd || !peer.up {
			continue
		}
		pt, ok := peer.transports[group]
		if !ok {
			continue
		}
		t.Connect(raft.ServerAddress(peer.id), pt)
		pt.Connect(raft.ServerAddress(nd.id), t)
	}
	return t
}

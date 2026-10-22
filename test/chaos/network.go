package chaos

import (
	"context"
	"fmt"
)

// Network removes nodes from the internode network of the cluster and
// reconnects them.
type Network struct {
	cluster *Cluster
	name    string
}

// NewNetwork returns the internode network of the cluster's project,
// unio-4x4_internal for the default project.
func NewNetwork(c *Cluster) *Network {
	return &Network{cluster: c, name: c.project + "_internal"}
}

// Name returns the Docker network name.
func (n *Network) Name() string {
	return n.name
}

// Disconnect detaches the container of service from the internode network.
func (n *Network) Disconnect(ctx context.Context, service string) error {
	return n.change(ctx, "disconnect", service)
}

// Connect attaches the container of service to the internode network.
func (n *Network) Connect(ctx context.Context, service string) error {
	return n.change(ctx, "connect", service)
}

func (n *Network) change(ctx context.Context, verb, service string) error {
	id, err := n.cluster.ContainerID(ctx, service)
	if err != nil {
		return err
	}
	_, err = n.cluster.docker(ctx, "network", verb, n.name, id)
	return err
}

// Partition cuts one node off the internode network until Heal is called.
// Tests register Heal in t.Cleanup so the node is reconnected even when the
// scenario fails. A Partition is not safe for concurrent use.
type Partition struct {
	network *Network
	service string
	cut     bool
}

// NewPartition returns a Partition for service on network n. It starts healed.
func NewPartition(n *Network, service string) *Partition {
	return &Partition{network: n, service: service}
}

// Cut disconnects the node. It is an error to cut an already cut partition.
func (p *Partition) Cut(ctx context.Context) error {
	if p.cut {
		return fmt.Errorf("chaos: %s is already partitioned", p.service)
	}
	if err := p.network.Disconnect(ctx, p.service); err != nil {
		return err
	}
	p.cut = true
	return nil
}

// Heal reconnects the node. It does nothing when the node is not cut, and it
// stays cut when the reconnect fails so that a later Heal can retry.
func (p *Partition) Heal(ctx context.Context) error {
	if !p.cut {
		return nil
	}
	if err := p.network.Connect(ctx, p.service); err != nil {
		return err
	}
	p.cut = false
	return nil
}

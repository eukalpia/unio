package server

import (
	"fmt"
	"log/slog"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/pki"
	"github.com/eukalpia/unio/internal/cluster/uniwire"
	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/ids"
)

// startTransport issues this node's certificate from the cluster CA and
// creates the UniWire transport. Every internode connection is mutual TLS 1.3
// between certificates of that CA, and a peer is identified by its
// certificate, never by what it sends. The node certificate is renewed in the
// background until the issuer is closed.
func startTransport(lc *lifecycle, cfg *config.Config, id identity, budget budgets, logger *slog.Logger) (*uniwire.Transport, error) {
	ca, err := pki.DeriveCA([]byte(cfg.Cluster.Secret.Expose()))
	if err != nil {
		return nil, fmt.Errorf("server: cluster CA: %w", err)
	}
	issuer, err := ca.NewIssuer(id.node, []string{cfg.Node.ID}, pki.IssuerOptions{})
	if err != nil {
		return nil, fmt.Errorf("server: node certificate: %w", err)
	}
	lc.onClose("certificate issuer", issuer.Close)

	serverTLS, err := pki.ServerConfig(issuer.Identity())
	if err != nil {
		return nil, fmt.Errorf("server: internode server TLS: %w", err)
	}
	clientTLS, err := pki.ClientConfig(issuer.Identity())
	if err != nil {
		return nil, fmt.Errorf("server: internode client TLS: %w", err)
	}
	tr, err := uniwire.New(uniwire.Config{
		Server:  serverTLS,
		Client:  clientTLS,
		Resolve: resolver(id.clusterMap),
		Limits:  transportLimits(budget),
		Logger:  logger.With("component", "uniwire"),
	})
	if err != nil {
		return nil, fmt.Errorf("server: internode transport: %w", err)
	}
	lc.onClose("internode transport", tr.Close)
	return tr, nil
}

// transportLimits returns the default UniWire limits with stream buffers
// drawn from the node's memory budget: streams this node sends and replies it
// serves use separate budgets.
func transportLimits(budget budgets) uniwire.Limits {
	limits := uniwire.DefaultLimits()
	limits.StreamBudget = budget.transportOut
	limits.ServeBudget = budget.transportServe
	return limits
}

// resolver maps a node id to its internode address from the cluster map.
// The map is fixed for the life of the process, so the lookup table is built
// once.
func resolver(m *cluster.Map) func(ids.NodeID) (string, error) {
	addrs := make(map[ids.NodeID]string, len(m.Nodes))
	for _, n := range m.Nodes {
		addrs[n.ID] = n.Addr
	}
	return func(id ids.NodeID) (string, error) {
		addr, ok := addrs[id]
		if !ok {
			return "", fmt.Errorf("server: node %q is not in the cluster map", id)
		}
		return addr, nil
	}
}

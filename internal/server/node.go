package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/eukalpia/unio/internal/config"
)

// metricsInterval is how often gauges that have no event of their own, such
// as buffer use and the deletion backlog, are sampled.
const metricsInterval = 5 * time.Second

// Node is a started Unio node.
type Node struct {
	cfg    *config.Config
	logger *slog.Logger
	lc     *lifecycle
	front  *frontEnd
}

// Start brings up a node with cfg, a configuration returned by config.Load.
// It returns once every component runs and the S3 listener is bound; requests
// are served by Serve. If any step fails, everything started before it is
// released and the error is returned.
func Start(ctx context.Context, cfg *config.Config, logger *slog.Logger) (_ *Node, err error) {
	lc := newLifecycle(logger)
	defer func() {
		if err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), cfg.Timeouts.Shutdown)
			defer cancel()
			if stopErr := lc.shutdown(stopCtx); stopErr != nil {
				logger.Error("release after failed start", "error", stopErr.Error())
			}
		}
	}()

	id, err := resolveIdentity(cfg)
	if err != nil {
		return nil, err
	}
	logger.Info("starting node", "node", string(id.node), "cluster", id.clusterID,
		"nodes", len(id.clusterMap.Nodes), "sets", len(id.clusterMap.Sets))

	// The memory budget bounds the buffers of every component, so the node's
	// memory use for data in flight never exceeds the configured amount. It
	// is split by layer; see budgets.
	budget := splitBudget(int64(cfg.Storage.MemoryBudget))

	drives, err := openDrives(ctx, lc, cfg, id, budget.drives, logger)
	if err != nil {
		return nil, err
	}
	tr, err := startTransport(lc, cfg, id, budget, logger)
	if err != nil {
		return nil, err
	}
	cs, err := startCluster(ctx, lc, cfg, id, tr, drives, logger)
	if err != nil {
		return nil, err
	}
	dp, err := startData(lc, cfg, id, cs, budget, logger)
	if err != nil {
		return nil, err
	}
	front, err := startFront(lc, cfg, cs, dp, drives, budget, logger)
	if err != nil {
		return nil, err
	}
	if err := startPprof(lc, cfg, logger); err != nil {
		return nil, err
	}
	startBackground(lc, cs, dp)
	lc.goBackground("metrics sampler", func(ctx context.Context) {
		sampleMetrics(ctx, front, dp, budget)
	})
	return &Node{cfg: cfg, logger: logger, lc: lc, front: front}, nil
}

// S3Addr returns the address the S3 endpoint listens on.
func (n *Node) S3Addr() net.Addr {
	return n.front.listener.Addr()
}

// Serve serves S3 requests until ctx is done, then drains them within the
// shutdown timeout and stops the node. It returns the first error of serving
// or stopping.
func (n *Node) Serve(ctx context.Context) error {
	n.logger.Info("serving S3", "address", n.S3Addr().String())
	serveErr := n.front.s3.Serve(ctx, n.front.listener)
	if serveErr != nil {
		n.logger.Error("S3 server stopped", "error", serveErr.Error())
	}
	return errors.Join(serveErr, n.Close())
}

// Close stops the node: the background workers, the object engine, the
// metadata store (which first hands over the Raft leaderships this node
// holds), the internode services and the drives, in that order. It is
// bounded by the shutdown timeout and safe to call more than once.
func (n *Node) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), n.cfg.Timeouts.Shutdown)
	defer cancel()
	if err := n.lc.shutdown(ctx); err != nil {
		return err
	}
	n.logger.Info("node stopped")
	return nil
}

// Run starts a node with cfg and serves until ctx is done.
func Run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	n, err := Start(ctx, cfg, logger)
	if err != nil {
		return err
	}
	return n.Serve(ctx)
}

// sampleMetrics updates the gauges that have no event of their own until ctx
// is done.
func sampleMetrics(ctx context.Context, front *frontEnd, dp *dataPath, budget budgets) {
	t := time.NewTicker(metricsInterval)
	defer t.Stop()
	for {
		front.metrics.SetBufpoolInUse(budget.inUse())
		front.metrics.SetGCQueueDepth(int64(dp.engine.Stats().GCPending))
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

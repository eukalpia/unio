package server

import (
	"fmt"
	"log/slog"

	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/erasure/healer"
	"github.com/eukalpia/unio/internal/erasure/placement"
	"github.com/eukalpia/unio/internal/object/engine"
)

// dataPath is the object layer and the erasure sets under it.
type dataPath struct {
	registry *placement.Registry
	healer   *healer.Healer
	engine   *engine.Engine
}

// startData builds the erasure sets of the cluster map over the drives the
// topology provides, local or remote, and the object engine over them and the
// metadata store. A drive that cannot be reached is an offline slot, which
// erasure coding tolerates up to the parity count.
func startData(lc *lifecycle, cfg *config.Config, id identity, cs *clusterServices, budget budgets, logger *slog.Logger) (*dataPath, error) {
	registry, err := placement.New(id.clusterMap, cs.topology, placement.Options{
		Budget: budget.erasure,
		Logger: logger.With("component", "placement"),
	})
	if err != nil {
		return nil, fmt.Errorf("server: erasure sets: %w", err)
	}
	heal := healer.New(registry.Sets(), healer.Options{
		Workers: cfg.Limits.BackgroundConcurrency,
		Logger:  logger.With("component", "healer"),
	})
	eng, err := engine.New(cs.store, registry, engine.Options{
		InlineThreshold: inlineThreshold(cfg.Storage.InlineThreshold),
		Logger:          logger.With("component", "engine"),
		GCWorkers:       cfg.Limits.BackgroundConcurrency,
		Budget:          budget.engine,
	})
	if err != nil {
		return nil, fmt.Errorf("server: object engine: %w", err)
	}
	// The engine drains its deletion queue on close, so it stops after the
	// front end and before the metadata store and the drives it writes to.
	lc.onStop("object engine", eng.Close)
	return &dataPath{registry: registry, healer: heal, engine: eng}, nil
}

// inlineThreshold converts the configured inline threshold, where 0 disables
// the inline tier, to the engine's option, where 0 selects the default and a
// negative value disables the tier.
func inlineThreshold(size config.Size) int64 {
	if size == 0 {
		return -1
	}
	return int64(size)
}

// startBackground starts the workers that keep the node healthy: peer
// probing, capacity refresh for placement, and shard healing. They stop
// before the components they use are released.
func startBackground(lc *lifecycle, cs *clusterServices, dp *dataPath) {
	lc.goBackground("peer health", cs.topology.Run)
	lc.goBackground("capacity refresh", dp.registry.Run)
	lc.goBackground("shard healing", dp.healer.Run)
	lc.onStop("background workers", lc.stopBackground)
}

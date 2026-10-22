// Package raftstore implements meta.Store and meta.SystemKV on Multi-Raft
// partitions backed by Pebble.
//
// Buckets and system key-value entries live in the system group (partition
// 0). Object versions and multipart state live in data partitions, chosen by
// cluster.PartitionOf(bucket, key). Every operation is linearizable: writes
// are applied through Raft, and reads are served by a partition leader after
// it confirms its leadership and has applied everything committed before the
// read (see package group).
package raftstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/fsm"
	"github.com/eukalpia/unio/internal/meta/raftstore/group"
	"github.com/eukalpia/unio/internal/meta/raftstore/logstore"
	"github.com/eukalpia/unio/internal/meta/raftstore/router"
)

// Store is the metadata plane of one node. It implements meta.Store and
// meta.SystemKV. It is safe for concurrent use.
type Store struct {
	cfg       Config
	opts      Options
	parts     map[ids.PartitionID]cluster.PartitionInfo
	dataParts []ids.PartitionID
	cache     *pebble.Cache
	logs      *logstore.Store
	fsms      map[ids.PartitionID]*fsm.FSM
	groups    map[ids.PartitionID]*group.Group
	router    *router.Router
}

// Compile-time interface checks.
var (
	_ meta.Store    = (*Store)(nil)
	_ meta.SystemKV = (*Store)(nil)
)

// Config configures a node's metadata store.
type Config struct {
	// Node is this node's id.
	Node ids.NodeID
	// Map is the cluster map. The store uses it as the placement of every
	// partition; a change of map requires a restart.
	Map *cluster.Map
	// DataDir holds the Raft logs, the state machine databases and the
	// snapshots. It is owned by the store.
	DataDir string
	// RPC carries forwarded commands and reads between nodes.
	RPC cluster.RPC
	// Net provides the Raft transports of the groups.
	Net cluster.RaftNetwork
	// Options tunes timeouts, snapshots and caches. The zero value selects
	// the defaults.
	Options Options
}

// Options tunes the store. Zero values select defaults.
type Options struct {
	// ApplyTimeout bounds how long a proposal waits to be queued.
	// Default 5s.
	ApplyTimeout time.Duration
	// HeartbeatTimeout is Raft's base timeout: the follower heartbeat timeout
	// and election timeout, with the leader lease at half of it. Default
	// 1s, the hashicorp default. Tests lower it to speed up elections.
	HeartbeatTimeout time.Duration
	// SnapshotInterval is how often Raft checks whether to snapshot.
	// Default 2m.
	SnapshotInterval time.Duration
	// SnapshotThreshold is the number of log entries that triggers a
	// snapshot. Default 8192.
	SnapshotThreshold uint64
	// CacheSize is the Pebble block cache size in bytes, shared by all
	// databases of the node. Default 64 MiB.
	CacheSize int64
	// Log receives Raft diagnostics. Nil discards them.
	Log io.Writer
}

// Defaults applied by Options.withDefaults.
const (
	defaultApplyTimeout      = 5 * time.Second
	defaultSnapshotInterval  = 2 * time.Minute
	defaultSnapshotThreshold = 8192
	defaultCacheSize         = 64 << 20
	snapshotsRetained        = 2
)

// withDefaults fills zero fields with their defaults.
func (o Options) withDefaults() Options {
	if o.ApplyTimeout <= 0 {
		o.ApplyTimeout = defaultApplyTimeout
	}
	if o.SnapshotInterval <= 0 {
		o.SnapshotInterval = defaultSnapshotInterval
	}
	if o.SnapshotThreshold == 0 {
		o.SnapshotThreshold = defaultSnapshotThreshold
	}
	if o.CacheSize <= 0 {
		o.CacheSize = defaultCacheSize
	}
	return o
}

// validate reports whether the configuration can start a store.
func (c Config) validate() error {
	switch {
	case c.Node == "":
		return errors.New("raftstore: node id is required")
	case c.Map == nil:
		return errors.New("raftstore: cluster map is required")
	case c.DataDir == "":
		return errors.New("raftstore: data directory is required")
	case c.RPC == nil:
		return errors.New("raftstore: RPC is required")
	case c.Net == nil:
		return errors.New("raftstore: raft network is required")
	}
	if len(c.Map.Partitions) < 2 {
		return fmt.Errorf("raftstore: the map needs a system group and at least one data partition, has %d partitions", len(c.Map.Partitions))
	}
	for i, p := range c.Map.Partitions {
		if p.ID != ids.PartitionID(i) {
			return fmt.Errorf("raftstore: partition at index %d has id %d; ids must be 0..n-1 in order", i, p.ID)
		}
	}
	return nil
}

// Open starts the store: it opens the databases, starts the Raft groups that
// this node hosts, and bootstraps the groups that have no state yet. It does
// not wait for leaders; operations wait on the router.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &Store{
		cfg:    cfg,
		opts:   cfg.Options.withDefaults(),
		parts:  make(map[ids.PartitionID]cluster.PartitionInfo, len(cfg.Map.Partitions)),
		fsms:   make(map[ids.PartitionID]*fsm.FSM),
		groups: make(map[ids.PartitionID]*group.Group),
	}
	for _, info := range cfg.Map.Partitions {
		s.parts[info.ID] = info
		if info.ID != ids.SystemPartition {
			s.dataParts = append(s.dataParts, info.ID)
		}
	}
	if err := s.openAll(ctx); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

// openAll opens the databases and starts the local replicas. On error the
// caller closes the store.
func (s *Store) openAll(ctx context.Context) error {
	// Metadata lists every bucket and key, so the directory is private to its
	// owner. Subdirectories are created inside it and inherit that protection.
	if err := os.MkdirAll(s.cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("raftstore: create data dir: %w", err)
	}
	s.cache = pebble.NewCache(s.opts.CacheSize)
	logs, err := logstore.Open(filepath.Join(s.cfg.DataDir, "raft"), s.cache)
	if err != nil {
		s.cache.Unref()
		s.cache = nil
		return fmt.Errorf("raftstore: open raft logs: %w", err)
	}
	s.logs = logs
	s.router, err = router.New(s.cfg.Node, s.cfg.RPC, s.replicasOf)
	if err != nil {
		return err
	}
	for _, info := range s.cfg.Map.Partitions {
		if !s.hosts(info) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.startReplica(info); err != nil {
			return err
		}
	}
	return nil
}

// hosts reports whether this node runs a replica of the partition.
func (s *Store) hosts(info cluster.PartitionInfo) bool {
	return contains(info.Replicas, s.cfg.Node) || contains(info.NonVoters, s.cfg.Node)
}

// startReplica opens the state machine and starts the Raft group of one
// partition on this node.
func (s *Store) startReplica(info cluster.PartitionInfo) error {
	name := strconv.FormatUint(uint64(info.ID), 10)
	kind := fsm.KindData
	if info.ID == ids.SystemPartition {
		kind = fsm.KindSystem
	}
	f, err := fsm.Open(filepath.Join(s.cfg.DataDir, "fsm", name), s.cache, kind)
	if err != nil {
		return fmt.Errorf("raftstore: partition %d: %w", info.ID, err)
	}
	s.fsms[info.ID] = f
	transport, err := s.cfg.Net.Transport(info.ID)
	if err != nil {
		return fmt.Errorf("raftstore: partition %d transport: %w", info.ID, err)
	}
	snaps, err := raft.NewFileSnapshotStore(filepath.Join(s.cfg.DataDir, "snapshots", name), snapshotsRetained, io.Discard)
	if err != nil {
		return fmt.Errorf("raftstore: partition %d snapshots: %w", info.ID, err)
	}
	logs := s.logs.Group(info.ID)
	g, err := group.Start(group.Config{
		Partition:         info.ID,
		Local:             s.cfg.Node,
		Voters:            info.Replicas,
		NonVoters:         info.NonVoters,
		Transport:         transport,
		Logs:              logs,
		Stable:            logs,
		Snapshots:         snaps,
		FSM:               f,
		ApplyTimeout:      s.opts.ApplyTimeout,
		SnapshotInterval:  s.opts.SnapshotInterval,
		SnapshotThreshold: s.opts.SnapshotThreshold,
		HeartbeatTimeout:  s.opts.HeartbeatTimeout,
		Log:               s.cfg.Options.Log,
	})
	if err != nil {
		return err
	}
	s.groups[info.ID] = g
	s.router.AddGroup(info.ID, g)
	return nil
}

// Close stops the Raft groups, then closes the state machines, the logs and
// the cache. It returns the first error and is safe to call more than once.
func (s *Store) Close() error {
	var errs []error
	for _, g := range s.groups {
		errs = append(errs, g.Close())
	}
	s.groups = map[ids.PartitionID]*group.Group{}
	for _, f := range s.fsms {
		errs = append(errs, f.Close())
	}
	s.fsms = map[ids.PartitionID]*fsm.FSM{}
	if s.logs != nil {
		errs = append(errs, s.logs.Close())
		s.logs = nil
	}
	if s.cache != nil {
		s.cache.Unref()
		s.cache = nil
	}
	return errors.Join(errs...)
}

// replicasOf returns the nodes that host a partition, voters first.
func (s *Store) replicasOf(p ids.PartitionID) []ids.NodeID {
	info, ok := s.parts[p]
	if !ok {
		return nil
	}
	out := make([]ids.NodeID, 0, len(info.Replicas)+len(info.NonVoters))
	out = append(out, info.Replicas...)
	return append(out, info.NonVoters...)
}

// partitionOf returns the data partition of a key in a bucket.
func (s *Store) partitionOf(bucket ids.BucketID, key string) ids.PartitionID {
	return cluster.PartitionOf(bucket, key, len(s.dataParts))
}

// contains reports whether n is in list.
func contains(list []ids.NodeID, n ids.NodeID) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

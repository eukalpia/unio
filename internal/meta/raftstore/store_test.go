package raftstore_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/metatest"
	"github.com/eukalpia/unio/internal/meta/raftstore"
)

// testDataPartitions is the number of data partitions of test clusters.
const testDataPartitions = 4

// testHeartbeat keeps elections fast in tests.
const testHeartbeat = 100 * time.Millisecond

// testCluster is a set of raft-backed stores that share one in-process
// network. Its methods are safe for concurrent use.
type testCluster struct {
	t     testing.TB
	net   *metatest.Cluster
	nodes []ids.NodeID
	m     *cluster.Map
	dirs  map[ids.NodeID]string
	opts  raftstore.Options

	mu     sync.Mutex
	stores map[ids.NodeID]*raftstore.Store
}

// openTestCluster starts n nodes with the test heartbeat and registers their
// cleanup with t.
func openTestCluster(t testing.TB, n int) *testCluster {
	t.Helper()
	return openTestClusterWith(t, n, raftstore.Options{HeartbeatTimeout: testHeartbeat})
}

// openTestClusterWith starts n nodes with opts and registers their cleanup
// with t.
func openTestClusterWith(t testing.TB, n int, opts raftstore.Options) *testCluster {
	t.Helper()
	c := &testCluster{
		t:      t,
		net:    metatest.NewCluster(n),
		dirs:   make(map[ids.NodeID]string),
		opts:   opts,
		stores: make(map[ids.NodeID]*raftstore.Store),
	}
	for i := 1; i <= n; i++ {
		c.nodes = append(c.nodes, ids.NodeID(fmt.Sprintf("n%d", i)))
	}
	c.m = buildMap(c.nodes, testDataPartitions)
	for _, node := range c.nodes {
		c.dirs[node] = t.TempDir()
		if err := c.open(node); err != nil {
			t.Fatalf("open store %s: %v", node, err)
		}
	}
	t.Cleanup(c.closeAll)
	return c
}

// open starts the store of one node from its data directory.
func (c *testCluster) open(node ids.NodeID) error {
	s, err := raftstore.Open(context.Background(), raftstore.Config{
		Node:    node,
		Map:     c.m,
		DataDir: c.dirs[node],
		RPC:     c.net.RPC(node),
		Net:     c.net.RaftNetwork(node),
		Options: c.opts,
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.stores[node] = s
	c.mu.Unlock()
	return nil
}

// kill makes a node unreachable and stops its store, as a crash would. Its
// data directory is kept, so restart recovers the node from disk.
func (c *testCluster) kill(node ids.NodeID) error {
	if err := c.net.Kill(node); err != nil {
		return fmt.Errorf("kill %s: %w", node, err)
	}
	c.mu.Lock()
	s := c.stores[node]
	delete(c.stores, node)
	c.mu.Unlock()
	if s != nil {
		if err := s.Close(); err != nil {
			return fmt.Errorf("close %s: %w", node, err)
		}
	}
	return nil
}

// restart brings a killed node back, recovering its store from disk.
func (c *testCluster) restart(node ids.NodeID) error {
	if err := c.net.Restart(node); err != nil {
		return fmt.Errorf("restart %s: %w", node, err)
	}
	return c.open(node)
}

// closeAll closes every running store. It takes every node off the network
// first. Otherwise a store that stops while its peers still send it RPCs waits
// for each RPC to time out.
func (c *testCluster) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for node := range c.stores {
		if err := c.net.Kill(node); err != nil {
			c.t.Errorf("take down %s: %v", node, err)
		}
	}
	for node, s := range c.stores {
		if err := s.Close(); err != nil {
			c.t.Errorf("close store %s: %v", node, err)
		}
		delete(c.stores, node)
	}
}

// store returns the store of the first node that is running. Operations on
// any node are linearizable, so one store is enough for the conformance
// suites.
func (c *testCluster) store() *raftstore.Store {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, node := range c.nodes {
		if s, ok := c.stores[node]; ok {
			return s
		}
	}
	return nil
}

// buildMap places the system group on the first three nodes as voters, with
// the remaining nodes as non-voters, and places each data partition on
// three nodes chosen in rotation.
func buildMap(nodes []ids.NodeID, data int) *cluster.Map {
	width := min(3, len(nodes))
	var nonVoters []ids.NodeID
	if len(nodes) > width {
		nonVoters = append(nonVoters, nodes[width:]...)
	}
	m := &cluster.Map{Version: 1, ClusterID: "test"}
	m.Partitions = append(m.Partitions, cluster.PartitionInfo{
		ID:        ids.SystemPartition,
		Replicas:  append([]ids.NodeID(nil), nodes[:width]...),
		NonVoters: nonVoters,
	})
	for p := 1; p <= data; p++ {
		reps := make([]ids.NodeID, 0, width)
		for i := 0; i < width; i++ {
			reps = append(reps, nodes[(p+i)%len(nodes)])
		}
		m.Partitions = append(m.Partitions, cluster.PartitionInfo{ID: ids.PartitionID(p), Replicas: reps})
	}
	return m
}

func TestStoreConformanceSingleNode(t *testing.T) {
	metatest.RunStoreSuite(t, func(t *testing.T) meta.Store {
		return openTestCluster(t, 1).store()
	})
}

func TestSystemKVConformanceSingleNode(t *testing.T) {
	metatest.RunSystemKVSuite(t, func(t *testing.T) meta.SystemKV {
		return openTestCluster(t, 1).store()
	})
}

func TestStoreConformanceThreeNodes(t *testing.T) {
	metatest.RunStoreSuite(t, func(t *testing.T) meta.Store {
		return openTestCluster(t, 3).store()
	})
}

func TestSystemKVConformanceThreeNodes(t *testing.T) {
	metatest.RunSystemKVSuite(t, func(t *testing.T) meta.SystemKV {
		return openTestCluster(t, 3).store()
	})
}

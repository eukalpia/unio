package raftstore_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/raftstore"
)

// electionWait bounds how long a test waits for a leader or for readiness.
const electionWait = 30 * time.Second

// handoverBkt is the bucket that tests write objects into.
const handoverBkt = "handover-bucket"

// awaitAllReady waits until every running store is ready.
func awaitAllReady(t *testing.T, c *testCluster) {
	t.Helper()
	for _, node := range c.nodes {
		s := c.storeOf(node)
		if s == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), electionWait)
		err := s.WaitReady(ctx)
		cancel()
		if err != nil {
			t.Fatalf("store %s did not become ready: %v", node, err)
		}
	}
}

// storeOf returns the store of node, or nil when the node is not running.
func (c *testCluster) storeOf(node ids.NodeID) *raftstore.Store {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stores[node]
}

// TestReadyAfterElectionSingleNode checks that a single node becomes ready
// once its group elects it, and that WaitReady returns.
func TestReadyAfterElectionSingleNode(t *testing.T) {
	c := openTestCluster(t, 1)
	s := c.storeOf(c.nodes[0])
	ctx, cancel := context.WithTimeout(context.Background(), electionWait)
	defer cancel()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatalf("Ready after WaitReady: %v", err)
	}
	c.closeAll()
	goleak.VerifyNone(t)
}

// TestReadyAfterClose checks that a closed store is not ready, that WaitReady
// returns ErrClosed at once instead of waiting, and that Close can be repeated.
func TestReadyAfterClose(t *testing.T) {
	c := openTestCluster(t, 1)
	s := c.storeOf(c.nodes[0])
	awaitAllReady(t, c)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), electionWait)
	defer cancel()
	if err := s.Ready(ctx); !errors.Is(err, raftstore.ErrClosed) {
		t.Fatalf("Ready after Close = %v, want ErrClosed", err)
	}
	start := time.Now()
	if err := s.WaitReady(ctx); !errors.Is(err, raftstore.ErrClosed) {
		t.Fatalf("WaitReady after Close = %v, want ErrClosed", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("WaitReady after Close took %v, want an immediate return", d)
	}
	c.closeAll()
	goleak.VerifyNone(t)
}

// TestReadyReportsGroupsWithoutLeader kills the two other nodes of a three
// node cluster. The survivor loses the quorum of every group, so Ready must
// name the leaderless groups, and WaitReady must give up at its deadline.
func TestReadyReportsGroupsWithoutLeader(t *testing.T) {
	c := openTestCluster(t, 3)
	awaitAllReady(t, c)
	for _, node := range c.nodes[1:] {
		if err := c.kill(node); err != nil {
			t.Fatalf("kill %s: %v", node, err)
		}
	}
	s := c.storeOf(c.nodes[0])
	// The survivor keeps its leases for a moment, so poll until every group
	// has lost its leader.
	const want = "5 groups have no leader: 0, 1, 2, 3, 4"
	deadline := time.Now().Add(electionWait)
	for err := s.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), want); err = s.Ready(context.Background()) {
		if time.Now().After(deadline) {
			t.Fatalf("Ready = %v, want an error containing %q", err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*testHeartbeat)
	defer cancel()
	err := s.WaitReady(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitReady = %v, want a wrapped context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("WaitReady error %q does not contain %q", err, want)
	}
	c.closeAll()
	goleak.VerifyNone(t)
}

// TestReadyConcurrentWithClose calls Ready from several goroutines while
// Close runs in several goroutines. The race detector checks the accesses,
// every Close returns nil, and Ready reports ErrClosed afterwards.
func TestReadyConcurrentWithClose(t *testing.T) {
	c := openTestCluster(t, 3)
	awaitAllReady(t, c)
	s := c.storeOf(c.nodes[0])

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.Ready(context.Background())
			}
		}()
	}

	const closers = 3
	errs := make(chan error, closers)
	var wg sync.WaitGroup
	for i := 0; i < closers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Close()
		}()
	}
	wg.Wait()
	close(stop)
	readers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Close: %v", err)
		}
	}
	if err := s.Ready(context.Background()); !errors.Is(err, raftstore.ErrClosed) {
		t.Fatalf("Ready after concurrent Close = %v, want ErrClosed", err)
	}
	c.closeAll()
	goleak.VerifyNone(t)
}

package raftstore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore"
	"github.com/eukalpia/unio/internal/object"
)

// Lifecycle tests use a longer heartbeat than the other tests. With it, a
// group that loses its leader without a handover stays leaderless for well
// over the handover bound, so the tests can tell the two cases apart.
const (
	lifecycleHeartbeat = time.Second
	// handoverBound is the longest a group may take to get a new leader after
	// its leader closes with a handover. Raft's election timeout is at least
	// lifecycleHeartbeat, so the bound is half of the shortest one.
	handoverBound = lifecycleHeartbeat / 2
	// electionWait bounds how long a test waits for a leader or for readiness.
	electionWait = 30 * time.Second
	handoverKeys = 16
	handoverBkt  = "handover-bucket"
)

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

// runningStore returns the store of node, or nil when the node is not running.
func (c *testCluster) storeOf(node ids.NodeID) *raftstore.Store {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stores[node]
}

// stop closes the store of node with its leadership handover, then takes the
// node down in the network, as a process exit would. The node is removed from
// the cluster.
func (c *testCluster) stop(node ids.NodeID) error {
	c.mu.Lock()
	s := c.stores[node]
	delete(c.stores, node)
	c.mu.Unlock()
	if s == nil {
		return fmt.Errorf("stop %s: node is not running", node)
	}
	if err := s.Close(); err != nil {
		return fmt.Errorf("stop %s: %w", node, err)
	}
	return c.net.Kill(node)
}

// busiestLeader returns the running node that leads the most groups, and the
// groups it leads in partition order. Ties go to the lower node id.
func busiestLeader(t *testing.T, c *testCluster) (ids.NodeID, []ids.PartitionID) {
	t.Helper()
	var victim ids.NodeID
	var led []ids.PartitionID
	for _, node := range c.nodes {
		s := c.storeOf(node)
		if s == nil {
			continue
		}
		var own []ids.PartitionID
		for _, info := range c.m.Partitions {
			if l, ok := s.LeaderForTest(info.ID); ok && l == node {
				own = append(own, info.ID)
			}
		}
		if len(own) > len(led) {
			victim, led = node, own
		}
	}
	if len(led) == 0 {
		t.Fatal("no node leads a group")
	}
	return victim, led
}

// agreedLeader reports whether every running store names the same leader for
// partition p, and that leader is not gone.
func (c *testCluster) agreedLeader(p ids.PartitionID, gone ids.NodeID) bool {
	c.mu.Lock()
	stores := make([]*raftstore.Store, 0, len(c.stores))
	for _, s := range c.stores {
		stores = append(stores, s)
	}
	c.mu.Unlock()
	var leader ids.NodeID
	for i, s := range stores {
		l, ok := s.LeaderForTest(p)
		if !ok || l == gone || (i > 0 && l != leader) {
			return false
		}
		leader = l
	}
	return len(stores) > 0
}

// awaitNewLeaders waits until every group in groups has one agreed leader
// among the running stores. It returns the time since start, or an error when
// some group still has none after electionWait.
func awaitNewLeaders(c *testCluster, gone ids.NodeID, groups []ids.PartitionID, start time.Time) (time.Duration, error) {
	deadline := start.Add(electionWait)
	for _, p := range groups {
		for !c.agreedLeader(p, gone) {
			if time.Now().After(deadline) {
				return 0, fmt.Errorf("group %d has no new leader after %v", p, time.Since(start))
			}
			time.Sleep(time.Millisecond)
		}
	}
	return time.Since(start), nil
}

// handover takes victim down with closeNode and measures how long the
// remaining stores take to agree on a new leader for every group in groups.
// The clock starts before closeNode runs, and the measurement runs beside the
// close, so the result includes the handover and is not delayed by the
// shutdown that follows it. It returns the handover time and the time closeNode
// took.
func handover(c *testCluster, victim ids.NodeID, groups []ids.PartitionID, closeNode func(ids.NodeID) error) (elapsed, closed time.Duration, err error) {
	start := time.Now()
	type result struct {
		elapsed time.Duration
		err     error
	}
	done := make(chan result, 1)
	go func() {
		d, err := awaitNewLeaders(c, victim, groups, start)
		done <- result{d, err}
	}()
	closeErr := closeNode(victim)
	closed = time.Since(start)
	r := <-done
	if closeErr != nil {
		return 0, closed, closeErr
	}
	return r.elapsed, closed, r.err
}

// handoverKey and handoverValue name the objects written before a close and
// read after it.
func handoverKey(i int) string   { return fmt.Sprintf("handover/%02d", i) }
func handoverValue(i int) string { return fmt.Sprintf("payload-%02d", i) }

// writeHandoverObjects creates the bucket and acknowledges every object
// before the test closes a node.
func writeHandoverObjects(t *testing.T, c *testCluster) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), electionWait)
	defer cancel()
	s := c.storeOf(c.nodes[0])
	if _, err := s.CreateBucket(ctx, handoverBkt, object.MakeBucketOptions{Owner: "lifecycle"}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	for i := 0; i < handoverKeys; i++ {
		v := handoverValue(i)
		_, err := s.PutObject(ctx, meta.ObjectRecord{
			Bucket:  handoverBkt,
			Key:     handoverKey(i),
			Size:    int64(len(v)),
			ETag:    v,
			ModTime: time.Now(),
			Inline:  []byte(v),
		}, object.Conditions{})
		if err != nil {
			t.Fatalf("put %s: %v", handoverKey(i), err)
		}
	}
}

// checkHandoverObjects reads every object from every running store with the
// linearizable read path and checks that no acknowledged write is missing.
func checkHandoverObjects(t *testing.T, c *testCluster) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), electionWait)
	defer cancel()
	for _, node := range c.nodes {
		s := c.storeOf(node)
		if s == nil {
			continue
		}
		for i := 0; i < handoverKeys; i++ {
			rec, err := s.GetObject(ctx, handoverBkt, handoverKey(i), "")
			if err != nil {
				t.Fatalf("store %s: get %s: %v", node, handoverKey(i), err)
			}
			if got, want := string(rec.Inline), handoverValue(i); got != want {
				t.Fatalf("store %s: get %s = %q, want %q", node, handoverKey(i), got, want)
			}
		}
	}
}

// TestCloseHandsOverLeadership closes the node that leads the most groups.
// Every group it led must get a new leader on the remaining nodes within the
// handover bound, and no acknowledged write may be lost.
func TestCloseHandsOverLeadership(t *testing.T) {
	c := openTestClusterWith(t, 3, raftstore.Options{HeartbeatTimeout: lifecycleHeartbeat})
	awaitAllReady(t, c)
	writeHandoverObjects(t, c)

	victim, led := busiestLeader(t, c)
	elapsed, closed, err := handover(c, victim, led, c.stop)
	if err != nil {
		t.Fatalf("closing %s: %v", victim, err)
	}
	t.Logf("closed %s, which led %d groups, in %v: new leaders after %v (bound %v, election timeout at least %v)",
		victim, len(led), closed, elapsed, handoverBound, lifecycleHeartbeat)
	if elapsed > handoverBound {
		t.Fatalf("new leaders took %v, want at most %v", elapsed, handoverBound)
	}

	ctx, cancel := context.WithTimeout(context.Background(), electionWait)
	defer cancel()
	for _, node := range c.nodes {
		if s := c.storeOf(node); s != nil {
			if err := s.WaitReady(ctx); err != nil {
				t.Fatalf("store %s not ready after the handover: %v", node, err)
			}
		}
	}
	checkHandoverObjects(t, c)

	// The remaining nodes keep accepting writes.
	s := c.storeOf(c.nodes[0])
	if s == nil {
		s = c.storeOf(c.nodes[1])
	}
	if _, err := s.PutObject(ctx, meta.ObjectRecord{
		Bucket: handoverBkt, Key: "after-handover", Size: 1, ETag: "x", ModTime: time.Now(), Inline: []byte("x"),
	}, object.Conditions{}); err != nil {
		t.Fatalf("write after the handover: %v", err)
	}

	c.closeAll()
	goleak.VerifyNone(t)
}

// TestCloseWithoutHandoverMissesTheBound is the control for the test above.
// A node that stops without a handover leaves its groups leaderless until an
// election timeout passes, so the same measurement must exceed the bound.
func TestCloseWithoutHandoverMissesTheBound(t *testing.T) {
	c := openTestClusterWith(t, 3, raftstore.Options{HeartbeatTimeout: lifecycleHeartbeat})
	awaitAllReady(t, c)
	writeHandoverObjects(t, c)

	victim, led := busiestLeader(t, c)
	elapsed, _, err := handover(c, victim, led, c.kill)
	if err != nil {
		t.Fatalf("killing %s: %v", victim, err)
	}
	t.Logf("killed %s, which led %d groups: new leaders after %v (bound %v)", victim, len(led), elapsed, handoverBound)
	if elapsed <= handoverBound {
		t.Fatalf("without a handover new leaders took %v, within the bound %v: the handover test cannot tell the cases apart",
			elapsed, handoverBound)
	}
	c.closeAll()
	goleak.VerifyNone(t)
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

// TestCloseSurvivesFailedHandover gives the handover no time at all. Close
// must still return nil, and the groups must elect new leaders by themselves.
func TestCloseSurvivesFailedHandover(t *testing.T) {
	c := openTestClusterWith(t, 3, raftstore.Options{
		HeartbeatTimeout: testHeartbeat,
		TransferTimeout:  time.Nanosecond,
	})
	awaitAllReady(t, c)
	writeHandoverObjects(t, c)

	victim, led := busiestLeader(t, c)
	start := time.Now()
	if err := c.stop(victim); err != nil {
		t.Fatalf("close %s with no time for the handover: %v", victim, err)
	}
	if d := time.Since(start); d > electionWait {
		t.Fatalf("close took %v", d)
	}
	if _, err := awaitNewLeaders(c, victim, led, start); err != nil {
		t.Fatal(err)
	}
	checkHandoverObjects(t, c)
	c.closeAll()
	goleak.VerifyNone(t)
}

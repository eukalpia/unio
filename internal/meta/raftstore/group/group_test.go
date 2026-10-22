package group_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/hashicorp/raft"
	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/fsm"
	"github.com/eukalpia/unio/internal/meta/raftstore/group"
	"github.com/eukalpia/unio/internal/meta/raftstore/logstore"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// electionTimeout bounds how long a test waits for leadership. It is an
// upper bound only: tests return as soon as the event happens.
const electionTimeout = 30 * time.Second

// testNode is one in-process replica of the system group.
type testNode struct {
	id     ids.NodeID
	logs   *logstore.Store
	fsm    *fsm.FSM
	g      *group.Group
	tr     *raft.InmemTransport
	leader chan bool
}

// testCluster owns the replicas of one test.
type testCluster struct {
	t     *testing.T
	nodes []*testNode
	cache *pebble.Cache
}

// newTestCluster starts n replicas of the system group. The transports are
// connected in both directions before any replica starts.
func newTestCluster(t *testing.T, n int) *testCluster {
	t.Helper()
	c := &testCluster{t: t, cache: pebble.NewCache(8 << 20)}
	t.Cleanup(c.close)
	voters := make([]ids.NodeID, 0, n)
	for i := 1; i <= n; i++ {
		voters = append(voters, ids.NodeID(fmt.Sprintf("n%d", i)))
	}
	for _, id := range voters {
		_, tr := raft.NewInmemTransportWithTimeout(raft.ServerAddress(id), 2*time.Second)
		c.nodes = append(c.nodes, &testNode{id: id, tr: tr, leader: make(chan bool, 1024)})
	}
	for _, a := range c.nodes {
		for _, b := range c.nodes {
			if a != b {
				a.tr.Connect(raft.ServerAddress(b.id), b.tr)
			}
		}
	}
	for _, nd := range c.nodes {
		c.start(nd, voters)
	}
	return c
}

// start opens the stores of one replica and starts its group.
func (c *testCluster) start(nd *testNode, voters []ids.NodeID) {
	c.t.Helper()
	dir := c.t.TempDir()
	logs, err := logstore.Open(filepath.Join(dir, "raft"), c.cache)
	if err != nil {
		c.t.Fatalf("open logs: %v", err)
	}
	f, err := fsm.Open(filepath.Join(dir, "fsm"), c.cache, fsm.KindSystem)
	if err != nil {
		c.t.Fatalf("open fsm: %v", err)
	}
	snaps, err := raft.NewFileSnapshotStore(filepath.Join(dir, "snaps"), 2, io.Discard)
	if err != nil {
		c.t.Fatalf("snapshots: %v", err)
	}
	gl := logs.Group(ids.SystemPartition)
	g, err := group.Start(group.Config{
		Partition:        ids.SystemPartition,
		Local:            nd.id,
		Voters:           voters,
		Transport:        nd.tr,
		Logs:             gl,
		Stable:           gl,
		Snapshots:        snaps,
		FSM:              f,
		ApplyTimeout:     5 * time.Second,
		HeartbeatTimeout: 100 * time.Millisecond,
		LeaderNotify:     nd.leader,
	})
	if err != nil {
		c.t.Fatalf("start %s: %v", nd.id, err)
	}
	nd.logs, nd.fsm, nd.g = logs, f, g
}

// awaitLeader returns the first replica that reports leadership, using the
// raft leadership notifications rather than polling.
func (c *testCluster) awaitLeader() *testNode {
	c.t.Helper()
	cases := make([]reflect.SelectCase, 0, len(c.nodes)+1)
	for _, nd := range c.nodes {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(nd.leader)})
	}
	timer := time.NewTimer(electionTimeout)
	defer timer.Stop()
	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(timer.C)})
	for {
		chosen, recv, _ := reflect.Select(cases)
		if chosen == len(c.nodes) {
			c.t.Fatal("no leader elected before the deadline")
		}
		if !recv.Bool() {
			continue
		}
		// A true notification means this replica became leader; confirm it
		// is still the leader before routing requests to it.
		nd := c.nodes[chosen]
		if nd.g.IsLeader() {
			return nd
		}
	}
}

// close stops every replica and releases the shared cache.
func (c *testCluster) close() {
	for _, nd := range c.nodes {
		if nd.g != nil {
			_ = nd.g.Close()
		}
		if nd.fsm != nil {
			_ = nd.fsm.Close()
		}
		if nd.logs != nil {
			_ = nd.logs.Close()
		}
	}
	c.cache.Unref()
}

// kvPut proposes a system key-value write and returns its revision.
func kvPut(ctx context.Context, nd *testNode, key, value string) (uint64, error) {
	cmd := &pb.Command{Cmd: &pb.Command_KvPut{KvPut: &pb.KVPut{Key: key, Value: []byte(value), ExpectRevision: meta.AnyRevision}}}
	data, err := cmd.MarshalVT()
	if err != nil {
		return 0, err
	}
	res, err := nd.g.Propose(context.Background(), data)
	if err != nil {
		return 0, err
	}
	return res.GetRevision(), nil
}

// kvGet reads a system key through the replica's linearizable read path.
func kvGet(ctx context.Context, nd *testNode, key string) (string, error) {
	q := &pb.ReadQuery{Query: &pb.ReadQuery_KvGet{KvGet: &pb.KVGetQuery{Key: key}}}
	res, err := nd.g.Read(ctx, q)
	if err != nil {
		return "", err
	}
	entries := res.GetKv().GetEntries()
	if len(entries) != 1 {
		return "", fmt.Errorf("got %d entries", len(entries))
	}
	return string(entries[0].GetValue()), nil
}

func TestLeaderServesLinearizableWrites(t *testing.T) {
	c := newTestCluster(t, 3)
	leader := c.awaitLeader()
	rev, err := kvPut(context.Background(), leader, "k", "v1")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if rev == 0 {
		t.Fatal("revision must be the non-zero raft index")
	}
	got, err := kvGet(context.Background(), leader, "k")
	if err != nil || got != "v1" {
		t.Fatalf("read = %q, %v; want v1", got, err)
	}
}

func TestFollowerRejectsProposalsAndReads(t *testing.T) {
	c := newTestCluster(t, 3)
	leader := c.awaitLeader()
	if _, err := kvPut(context.Background(), leader, "k", "v"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, nd := range c.nodes {
		if nd == leader {
			continue
		}
		if _, err := kvPut(context.Background(), nd, "x", "y"); !errors.Is(err, meta.ErrNotLeader) {
			t.Errorf("%s propose err = %v, want ErrNotLeader", nd.id, err)
		}
		if _, err := kvGet(context.Background(), nd, "k"); !errors.Is(err, meta.ErrNotLeader) {
			t.Errorf("%s read err = %v, want ErrNotLeader", nd.id, err)
		}
	}
}

func TestCommittedWriteSurvivesLeaderFailover(t *testing.T) {
	c := newTestCluster(t, 3)
	leader := c.awaitLeader()
	if _, err := kvPut(context.Background(), leader, "durable", "yes"); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if err := leader.g.Close(); err != nil {
		t.Fatalf("stop leader: %v", err)
	}
	var survivors []*testNode
	for _, nd := range c.nodes {
		if nd != leader {
			survivors = append(survivors, nd)
		}
	}
	next := c.awaitLeaderAmong(survivors)
	got, err := kvGet(context.Background(), next, "durable")
	if err != nil || got != "yes" {
		t.Fatalf("after failover read = %q, %v; want yes", got, err)
	}
}

// awaitLeaderAmong is awaitLeader restricted to a subset of replicas.
func (c *testCluster) awaitLeaderAmong(nodes []*testNode) *testNode {
	c.t.Helper()
	cases := make([]reflect.SelectCase, 0, len(nodes)+1)
	for _, nd := range nodes {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(nd.leader)})
	}
	timer := time.NewTimer(electionTimeout)
	defer timer.Stop()
	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(timer.C)})
	for {
		chosen, recv, _ := reflect.Select(cases)
		if chosen == len(nodes) {
			c.t.Fatal("no new leader elected before the deadline")
		}
		if recv.Bool() && nodes[chosen].g.IsLeader() {
			return nodes[chosen]
		}
	}
}

func TestConcurrentReadsAreLinearizable(t *testing.T) {
	c := newTestCluster(t, 3)
	leader := c.awaitLeader()
	if _, err := kvPut(context.Background(), leader, "hot", "value"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := kvGet(context.Background(), leader, "hot")
			if err == nil && got != "value" {
				err = fmt.Errorf("read %q", got)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent read: %v", err)
		}
	}
}

// TestCloseWithVerificationsInFlight closes a replica while concurrent reads
// are verifying leadership, then closes the rest of the cluster. hashicorp/raft
// can strand a VerifyLeader request that is queued as its raft instance stops,
// and a stranded request blocks its caller forever. Close must wait for the
// verifications it started, so that the goroutine check after each cluster
// finds nothing left behind.
func TestCloseWithVerificationsInFlight(t *testing.T) {
	for i := 0; i < 40; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			c := newTestCluster(t, 3)
			leader := c.awaitLeader()
			q := &pb.ReadQuery{Query: &pb.ReadQuery_KvGet{KvGet: &pb.KVGetQuery{Key: "x"}}}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			var wg sync.WaitGroup
			for j := 0; j < 8; j++ {
				for _, nd := range c.nodes {
					wg.Add(1)
					go func(g *group.Group) {
						defer wg.Done()
						for k := 0; k < 3; k++ {
							_, _ = g.Read(ctx, q)
						}
					}(nd.g)
				}
			}
			if err := leader.g.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			wg.Wait()
		})
		if err := goleak.Find(); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}

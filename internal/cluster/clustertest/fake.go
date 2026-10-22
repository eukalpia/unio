package clustertest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/uniwire"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

// FakeNetwork is an in-process network of cluster.RPC endpoints. A call runs
// the callee's handler directly. Requests and replies are marshalled, so that
// handlers see copies as they would on the wire, and errors are returned as
// they are, so that sentinels keep their identity. Partitions and isolation
// fail calls with uniwire.ErrUnavailable, as a dead peer would. Latency delays
// each call and honours the caller's context. FakeNetwork is safe for
// concurrent use.
type FakeNetwork struct {
	mu        sync.Mutex
	endpoints map[ids.NodeID]*FakeRPC
	cut       map[link]bool
	isolated  map[ids.NodeID]bool
	latency   time.Duration
}

// link is a directed pair of nodes.
type link struct{ from, to ids.NodeID }

// NewFakeNetwork returns an empty network with no latency and no partitions.
func NewFakeNetwork() *FakeNetwork {
	return &FakeNetwork{
		endpoints: map[ids.NodeID]*FakeRPC{},
		cut:       map[link]bool{},
		isolated:  map[ids.NodeID]bool{},
	}
}

// Endpoint returns the RPC endpoint of node id, creating it on first use.
func (n *FakeNetwork) Endpoint(id ids.NodeID) *FakeRPC {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.endpointLocked(id)
}

func (n *FakeNetwork) endpointLocked(id ids.NodeID) *FakeRPC {
	ep, ok := n.endpoints[id]
	if !ok {
		ep = &FakeRPC{net: n, id: id, handlers: map[string]cluster.Handler{}}
		n.endpoints[id] = ep
	}
	return ep
}

// Partition cuts traffic between a and b in both directions.
func (n *FakeNetwork) Partition(a, b ids.NodeID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cut[link{a, b}] = true
	n.cut[link{b, a}] = true
}

// Isolate cuts all traffic to and from id.
func (n *FakeNetwork) Isolate(id ids.NodeID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.isolated[id] = true
}

// Heal removes every partition and isolation.
func (n *FakeNetwork) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cut = map[link]bool{}
	n.isolated = map[ids.NodeID]bool{}
}

// SetLatency sets the delay applied to every call. Zero removes it.
func (n *FakeNetwork) SetLatency(d time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.latency = d
}

// blocked reports whether calls from one node to another are cut.
func (n *FakeNetwork) blocked(from, to ids.NodeID) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.cut[link{from, to}] || n.isolated[from] || n.isolated[to]
}

// target returns the endpoint of to and the current latency.
func (n *FakeNetwork) target(to ids.NodeID) (*FakeRPC, time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.endpoints[to], n.latency
}

// FakeRPC is one node's endpoint on a FakeNetwork. It implements cluster.RPC.
type FakeRPC struct {
	net *FakeNetwork
	id  ids.NodeID

	mu       sync.Mutex
	handlers map[string]cluster.Handler
}

// FakeRPC implements cluster.RPC.
var _ cluster.RPC = (*FakeRPC)(nil)

// Register implements cluster.RPC. A method name can be registered once.
func (r *FakeRPC) Register(method string, h cluster.Handler) error {
	if method == "" || h == nil {
		return fmt.Errorf("clustertest: invalid registration of %q", method)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.handlers[method]; dup {
		return fmt.Errorf("clustertest: method %q already registered on %s", method, r.id)
	}
	r.handlers[method] = h
	return nil
}

// handler returns the handler for method, or nil.
func (r *FakeRPC) handler(method string) cluster.Handler {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handlers[method]
}

// Call implements cluster.RPC.
func (r *FakeRPC) Call(ctx context.Context, to ids.NodeID, method string, req, resp cluster.Message) error {
	if r.net.blocked(r.id, to) {
		return fmt.Errorf("clustertest: %s to %s: %w: partitioned", method, to, uniwire.ErrUnavailable)
	}
	callee, latency := r.net.target(to)
	if latency > 0 {
		timer := time.NewTimer(latency)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	if callee == nil {
		return fmt.Errorf("clustertest: %s to %s: %w: no such node", method, to, uniwire.ErrUnavailable)
	}
	h := callee.handler(method)
	if h == nil {
		return fmt.Errorf("clustertest: %s on %s: %w", method, to, uniwire.ErrUnknownMethod)
	}
	payload, err := req.MarshalVT()
	if err != nil {
		return fmt.Errorf("clustertest: marshal %s: %w", method, err)
	}
	out, err := h(ctx, r.id, payload)
	if err != nil {
		return err
	}
	if err := resp.UnmarshalVT(out); err != nil {
		return fmt.Errorf("clustertest: decode %s reply: %w", method, err)
	}
	return nil
}

// FakeTopology is a cluster.Topology whose map, drives and health the test
// sets directly. It is safe for concurrent use.
type FakeTopology struct {
	local ids.NodeID

	mu     sync.Mutex
	m      *cluster.Map
	drives map[ids.DriveID]drive.Drive
	down   map[ids.NodeID]bool
}

// FakeTopology implements cluster.Topology.
var _ cluster.Topology = (*FakeTopology)(nil)

// NewFakeTopology returns a topology for node local over map m.
func NewFakeTopology(local ids.NodeID, m *cluster.Map) *FakeTopology {
	return &FakeTopology{
		local:  local,
		m:      m,
		drives: map[ids.DriveID]drive.Drive{},
		down:   map[ids.NodeID]bool{},
	}
}

// LocalNode implements cluster.Topology.
func (f *FakeTopology) LocalNode() ids.NodeID { return f.local }

// Map implements cluster.Topology.
func (f *FakeTopology) Map() *cluster.Map {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m
}

// SetMap replaces the cluster map.
func (f *FakeTopology) SetMap(m *cluster.Map) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m = m
}

// SetDrive makes d resolvable by its id.
func (f *FakeTopology) SetDrive(d drive.Drive) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drives[d.ID()] = d
}

// Drive implements cluster.Topology. An unset drive fails with
// drive.ErrOffline, as a drive that has not been opened would.
func (f *FakeTopology) Drive(id ids.DriveID) (drive.Drive, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.drives[id]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("clustertest: drive %s: %w", id, drive.ErrOffline)
}

// SetDown marks node as failing its health checks, or as healthy again.
func (f *FakeTopology) SetDown(node ids.NodeID, down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down[node] = down
}

// Healthy implements cluster.Topology.
func (f *FakeTopology) Healthy(node ids.NodeID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.down[node]
}

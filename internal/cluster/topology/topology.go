// Package topology is the node's view of the cluster. It holds the current
// cluster map, resolves drives to local engines or remote clients, and tracks
// node health with periodic liveness probes.
package topology

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/remotedrive"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// MethodPing is the liveness RPC every node serves. Probes call it.
const MethodPing = "topology.Ping"

// ErrUnknownDrive is returned by Drive for an id that the cluster map does not
// contain.
var ErrUnknownDrive = s3err.NewSentinel("topology: unknown drive", s3err.InternalError)

// Transport is the part of a UniWire transport that Topology uses: remote
// drive calls, the ping handler and probe calls.
type Transport interface {
	remotedrive.Transport
	Register(method string, h cluster.Handler) error
}

// Config configures a Topology.
type Config struct {
	// Local is this node's id. It must appear in Map.
	Local ids.NodeID
	// Transport carries probes and remote drive calls.
	Transport Transport
	// Map is the initial cluster map.
	Map *cluster.Map
	// ProbeInterval is the time between probe rounds. Default one second.
	ProbeInterval time.Duration
	// ProbeTimeout bounds one probe. Default half a second.
	ProbeTimeout time.Duration
	// FailureThreshold is the number of consecutive failed probes after which a
	// node is reported unhealthy. Default three.
	FailureThreshold int
	// MaxProbes bounds the probes in flight at once. Default sixteen.
	MaxProbes int
}

// Topology implements cluster.Topology. It is safe for concurrent use.
type Topology struct {
	cfg  Config
	mapv atomic.Pointer[cluster.Map]

	// mu guards the tables below.
	mu      sync.Mutex
	local   map[ids.DriveID]drive.Drive
	remotes map[ids.DriveID]remote
	health  map[ids.NodeID]*nodeHealth
}

// remote is a cached client for a drive hosted on another node.
type remote struct {
	node   ids.NodeID
	client *remotedrive.Client
}

// nodeHealth is the failure-detection state of one peer.
type nodeHealth struct {
	failures int
	down     bool
}

// Topology implements cluster.Topology.
var _ cluster.Topology = (*Topology)(nil)

// New returns a Topology and registers the ping handler on the transport.
func New(cfg Config) (*Topology, error) {
	if cfg.Local == "" {
		return nil, errors.New("topology: local node id is required")
	}
	if cfg.Transport == nil {
		return nil, errors.New("topology: transport is required")
	}
	if cfg.Map == nil {
		return nil, errors.New("topology: initial cluster map is required")
	}
	if !hasNode(cfg.Map, cfg.Local) {
		return nil, fmt.Errorf("topology: local node %s is not in the cluster map", cfg.Local)
	}
	if cfg.ProbeInterval <= 0 {
		cfg.ProbeInterval = time.Second
	}
	if cfg.ProbeTimeout <= 0 {
		cfg.ProbeTimeout = 500 * time.Millisecond
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.MaxProbes <= 0 {
		cfg.MaxProbes = 16
	}
	t := &Topology{
		cfg:     cfg,
		local:   map[ids.DriveID]drive.Drive{},
		remotes: map[ids.DriveID]remote{},
		health:  map[ids.NodeID]*nodeHealth{},
	}
	t.mapv.Store(cfg.Map)
	if err := cfg.Transport.Register(MethodPing, t.handlePing); err != nil {
		return nil, fmt.Errorf("topology: register %s: %w", MethodPing, err)
	}
	return t, nil
}

// LocalNode implements cluster.Topology.
func (t *Topology) LocalNode() ids.NodeID { return t.cfg.Local }

// Map implements cluster.Topology. The result must not be modified.
func (t *Topology) Map() *cluster.Map { return t.mapv.Load() }

// SetMap installs a newer cluster map. A map older than the current one is
// refused, so that a delayed update cannot roll the view back.
func (t *Topology) SetMap(m *cluster.Map) error {
	if m == nil {
		return errors.New("topology: nil cluster map")
	}
	if !hasNode(m, t.cfg.Local) {
		return fmt.Errorf("topology: new map omits local node %s", t.cfg.Local)
	}
	for {
		cur := t.mapv.Load()
		if m.Version < cur.Version {
			return fmt.Errorf("topology: map version %d is older than current %d", m.Version, cur.Version)
		}
		if t.mapv.CompareAndSwap(cur, m) {
			return nil
		}
	}
}

// AddDrive registers a drive hosted on this node. The cluster map must place
// the drive on the local node.
func (t *Topology) AddDrive(d drive.Drive) error {
	loc, ok := findDrive(t.Map(), d.ID())
	if !ok || loc.Node != t.cfg.Local {
		return fmt.Errorf("topology: drive %s is not placed on %s in the cluster map", d.ID(), t.cfg.Local)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.local[d.ID()] = d
	return nil
}

// Drive implements cluster.Topology. A drive on this node must have been
// added with AddDrive; drives on other nodes get a cached remote client.
func (t *Topology) Drive(id ids.DriveID) (drive.Drive, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if d, ok := t.local[id]; ok {
		return d, nil
	}
	loc, ok := findDrive(t.Map(), id)
	if !ok {
		return nil, fmt.Errorf("topology: drive %s: %w", id, ErrUnknownDrive)
	}
	if loc.Node == t.cfg.Local {
		return nil, fmt.Errorf("topology: local drive %s is not open: %w", id, drive.ErrOffline)
	}
	if r, ok := t.remotes[id]; ok && r.node == loc.Node {
		return r.client, nil
	}
	c := remotedrive.NewClient(t.cfg.Transport, loc.Node, id)
	t.remotes[id] = remote{node: loc.Node, client: c}
	return c, nil
}

// Healthy implements cluster.Topology. A node is healthy until it misses
// FailureThreshold consecutive probes, so a new node is not reported down
// before it has been probed. The local node is always healthy, and a node that
// is not in the map never is.
func (t *Topology) Healthy(node ids.NodeID) bool {
	if node == t.cfg.Local {
		return true
	}
	if !hasNode(t.Map(), node) {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	h, ok := t.health[node]
	return !ok || !h.down
}

// Run probes every peer every ProbeInterval until ctx is done. Each round
// runs at most MaxProbes probes at once and waits for them to finish, so the
// number of goroutines stays bounded. Run blocks; callers run it in their own
// goroutine.
func (t *Topology) Run(ctx context.Context) {
	tk := time.NewTicker(t.cfg.ProbeInterval)
	defer tk.Stop()
	for {
		t.probeRound(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// probeRound probes every peer once and returns when all probes are done.
func (t *Topology) probeRound(ctx context.Context) {
	m := t.Map()
	sem := make(chan struct{}, t.cfg.MaxProbes)
	var wg sync.WaitGroup
	defer wg.Wait()
	for _, nd := range m.Nodes {
		if nd.ID == t.cfg.Local {
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func(id ids.NodeID) {
			defer wg.Done()
			defer func() { <-sem }()
			t.probe(ctx, id)
		}(nd.ID)
	}
}

// probe sends one ping to id and records the outcome. A probe cut short by
// shutdown says nothing about the peer, so it is not recorded.
func (t *Topology) probe(ctx context.Context, id ids.NodeID) {
	pctx, cancel := context.WithTimeout(ctx, t.cfg.ProbeTimeout)
	defer cancel()
	var resp uniov1.PingResponse
	err := t.cfg.Transport.Call(pctx, id, MethodPing, &uniov1.PingRequest{}, &resp)
	if ctx.Err() != nil {
		return
	}
	t.record(id, err == nil)
}

// record folds one probe outcome into the health of id.
func (t *Topology) record(id ids.NodeID, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h, exists := t.health[id]
	if !exists {
		h = &nodeHealth{}
		t.health[id] = h
	}
	if ok {
		h.failures = 0
		h.down = false
		return
	}
	h.failures++
	if h.failures >= t.cfg.FailureThreshold {
		h.down = true
	}
}

// handlePing answers a liveness probe with this node's identity and map
// version.
func (t *Topology) handlePing(context.Context, ids.NodeID, []byte) ([]byte, error) {
	return (&uniov1.PingResponse{Node: string(t.cfg.Local), MapVersion: t.Map().Version}).MarshalVT()
}

// hasNode reports whether m lists node id.
func hasNode(m *cluster.Map, id ids.NodeID) bool {
	for _, n := range m.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

// findDrive returns the placement of drive id in m.
func findDrive(m *cluster.Map, id ids.DriveID) (cluster.DriveLocation, bool) {
	for _, d := range m.Drives {
		if d.ID == id {
			return d, true
		}
	}
	return cluster.DriveLocation{}, false
}

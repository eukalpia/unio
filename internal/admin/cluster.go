package admin

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

// Drive probes run at most driveProbeWorkers at a time, and all of them end
// within driveProbeTimeout, so that one stalled drive cannot hold up the view.
const (
	driveProbeWorkers = 8
	driveProbeTimeout = 2 * time.Second
)

// clusterView is the body of GET cluster.
type clusterView struct {
	ClusterID string       `json:"clusterId"`
	Nodes     []nodeView   `json:"nodes"`
	Sets      []setView    `json:"sets"`
	Capacity  capacityView `json:"capacity"`
}

// nodeView is one node of the cluster.
type nodeView struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	S3Address string `json:"s3Address"`
	Healthy   bool   `json:"healthy"`
}

// setView is one erasure set and its drives, in shard order.
type setView struct {
	ID           ids.SetID   `json:"id"`
	DataShards   int         `json:"dataShards"`
	ParityShards int         `json:"parityShards"`
	Drives       []driveView `json:"drives"`
}

// driveView is one drive. Capacity is zero for a drive that is offline.
type driveView struct {
	ID     string `json:"id"`
	Node   string `json:"node"`
	Path   string `json:"path"`
	Online bool   `json:"online"`
	Total  uint64 `json:"total"`
	Free   uint64 `json:"free"`
	Used   uint64 `json:"used"`
}

// capacityView is the capacity of the cluster: the sum over its filesystems.
// Drives of one node on the same filesystem count once, so pointing four
// drives at directories of one disk does not report four disks.
type capacityView struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`
}

// driveStat is the answer of one drive probe.
type driveStat struct {
	online            bool
	total, free, used uint64
	filesystem        string
}

// cluster answers GET cluster from the cluster map, the health table and a
// probe of every drive.
func (s *Service) cluster(c *call) (Response, error) {
	m := s.cfg.Cluster.Map()
	stats := s.probeDrives(c.ctx, m.Drives)
	locs := make(map[ids.DriveID]cluster.DriveLocation, len(m.Drives))
	view := clusterView{ClusterID: m.ClusterID, Nodes: make([]nodeView, 0, len(m.Nodes)), Sets: make([]setView, 0, len(m.Sets))}
	for _, d := range m.Drives {
		locs[d.ID] = d
	}
	view.Capacity = sumCapacity(m.Drives, stats)
	for _, n := range m.Nodes {
		view.Nodes = append(view.Nodes, nodeView{
			ID:        string(n.ID),
			Address:   n.Addr,
			S3Address: n.S3Addr,
			Healthy:   s.cfg.Cluster.Healthy(n.ID),
		})
	}
	for _, set := range m.Sets {
		sv := setView{ID: set.ID, DataShards: set.DataShards, ParityShards: set.ParityShards, Drives: make([]driveView, 0, len(set.Drives))}
		for _, id := range set.Drives {
			loc, ok := locs[id]
			if !ok {
				continue
			}
			st := stats[id]
			sv.Drives = append(sv.Drives, driveView{
				ID:     id.String(),
				Node:   string(loc.Node),
				Path:   loc.Path,
				Online: st.online,
				Total:  st.total,
				Free:   st.free,
				Used:   st.used,
			})
		}
		view.Sets = append(view.Sets, sv)
	}
	return jsonResponse(http.StatusOK, view)
}

// sumCapacity adds up the capacity of the filesystems behind drives. Drives
// of one node that report the same filesystem are counted once; a drive that
// reports none is counted on its own.
func sumCapacity(drives []cluster.DriveLocation, stats map[ids.DriveID]driveStat) capacityView {
	var out capacityView
	counted := make(map[string]bool, len(drives))
	for _, d := range drives {
		st := stats[d.ID]
		if st.filesystem != "" {
			fs := string(d.Node) + "\x00" + st.filesystem
			if counted[fs] {
				continue
			}
			counted[fs] = true
		}
		out.Total += st.total
		out.Used += st.used
		out.Free += st.free
	}
	return out
}

// probeDrives asks every drive for its capacity. The probes run in parallel,
// bounded by driveProbeWorkers and by one deadline of driveProbeTimeout. A
// drive that does not answer in time is reported offline. Every probe returns
// before probeDrives does.
func (s *Service) probeDrives(ctx context.Context, drives []cluster.DriveLocation) map[ids.DriveID]driveStat {
	ctx, cancel := context.WithTimeout(ctx, driveProbeTimeout)
	defer cancel()
	var mu sync.Mutex
	out := make(map[ids.DriveID]driveStat, len(drives))
	var g errgroup.Group
	g.SetLimit(driveProbeWorkers)
	for _, d := range drives {
		g.Go(func() error {
			st := s.probeDrive(ctx, d.ID)
			mu.Lock()
			out[d.ID] = st
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// probeDrive asks one drive for its identity and capacity. Any failure is an
// offline drive with no capacity.
func (s *Service) probeDrive(ctx context.Context, id ids.DriveID) driveStat {
	d, err := s.cfg.Cluster.Drive(id)
	if err != nil {
		return driveStat{}
	}
	info, err := d.Info(ctx)
	if err != nil || !info.Online {
		return driveStat{}
	}
	return driveStat{online: true, total: info.Total, free: info.Free, used: info.Used, filesystem: info.Filesystem}
}

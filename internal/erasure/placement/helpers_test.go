package placement

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/drivetest"
	"github.com/eukalpia/unio/internal/ids"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// driveID returns a deterministic, non-zero id for slot of set s.
func driveID(s, slot int) ids.DriveID {
	var id ids.DriveID
	id[0] = byte(s + 1)
	id[1] = byte(slot + 1)
	return id
}

// newMap returns a cluster map of sets erasure sets with k data and p parity
// shards each. Set i has the id i+1, and slot j of every set sits on node-j.
func newMap(sets, k, p int) *cluster.Map {
	m := &cluster.Map{Version: 1, ClusterID: "test"}
	for s := range sets {
		info := cluster.SetInfo{ID: ids.SetID(s + 1), DataShards: k, ParityShards: p}
		for slot := range k + p {
			id := driveID(s, slot)
			info.Drives = append(info.Drives, id)
			m.Drives = append(m.Drives, cluster.DriveLocation{
				ID:   id,
				Node: ids.NodeID(fmt.Sprintf("node-%d", slot)),
				Path: fmt.Sprintf("/data/set%d/slot%d", s+1, slot),
			})
		}
		m.Sets = append(m.Sets, info)
	}
	return m
}

// mapSource serves drives by id. An id it does not hold fails, as an
// unreachable drive would.
type mapSource map[ids.DriveID]drive.Drive

// Drive implements DriveSource.
func (s mapSource) Drive(id ids.DriveID) (drive.Drive, error) {
	d, ok := s[id]
	if !ok {
		return nil, fmt.Errorf("test: no drive %s", id)
	}
	return d, nil
}

// room is the capacity that every drive of a set reports.
type room struct {
	total, used, free uint64
}

// roomy is a set with plenty of space.
var roomy = room{total: 1 << 40, free: 1 << 40}

// capDrive wraps a drive and reports a configured capacity instead of its own,
// so that a test can make a set full or small without writing data.
type capDrive struct {
	drive.Drive
	room room
}

// Info implements drive.Drive.
func (d capDrive) Info(ctx context.Context) (drive.Info, error) {
	if err := ctx.Err(); err != nil {
		return drive.Info{}, err
	}
	return drive.Info{
		ID:     d.ID(),
		Total:  d.room.total,
		Used:   d.room.used,
		Free:   d.room.free,
		Online: true,
	}, nil
}

// hangDrive blocks in Info until its context is done, like a drive behind a
// partitioned link.
type hangDrive struct {
	drive.Drive
}

// Info implements drive.Drive.
func (hangDrive) Info(ctx context.Context) (drive.Info, error) {
	<-ctx.Done()
	return drive.Info{}, ctx.Err()
}

// countingDrive counts the Info calls made on the drive.
type countingDrive struct {
	drive.Drive
	calls func()
}

// Info implements drive.Drive.
func (d countingDrive) Info(ctx context.Context) (drive.Info, error) {
	d.calls()
	return d.Drive.Info(ctx)
}

// fixture is a cluster map and the drives that serve it.
type fixture struct {
	m   *cluster.Map
	src mapSource
}

// newFixture returns a map of sets erasure sets with k data and p parity
// shards, and a source of in-memory drives. Each set reports the room given in
// rooms, or roomy when rooms has no entry for it.
func newFixture(sets, k, p int, rooms map[ids.SetID]room) fixture {
	m := newMap(sets, k, p)
	src := make(mapSource)
	for _, s := range m.Sets {
		rm, ok := rooms[s.ID]
		if !ok {
			rm = roomy
		}
		for _, id := range s.Drives {
			src[id] = capDrive{Drive: drivetest.NewMemory(id), room: rm}
		}
	}
	return fixture{m: m, src: src}
}

// registry builds a registry over the fixture's map and drives.
func (f fixture) registry(t *testing.T, opts Options) *Registry {
	t.Helper()
	r, err := New(f.m, f.src, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// Package offline provides a drive.Drive placeholder for a drive that could not
// be opened or reached. Every data operation fails with drive.ErrOffline, so an
// erasure set can be built over a partly available cluster and treat the
// missing drive as a set of failed shards.
package offline

import (
	"context"
	"io"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

// Drive is a drive.Drive that is always offline. It is safe for concurrent
// use because it holds no mutable state.
type Drive struct {
	id       ids.DriveID
	node     ids.NodeID
	endpoint string
}

var _ drive.Drive = (*Drive)(nil)

// New returns an offline placeholder for drive id, which the cluster map places
// on node at endpoint.
func New(id ids.DriveID, node ids.NodeID, endpoint string) *Drive {
	return &Drive{id: id, node: node, endpoint: endpoint}
}

// ID implements drive.Drive.
func (d *Drive) ID() ids.DriveID { return d.id }

// WriteShard implements drive.Drive. It always fails with drive.ErrOffline.
func (d *Drive) WriteShard(context.Context, ids.ShardID, io.Reader, int64) error {
	return drive.ErrOffline
}

// ReadShard implements drive.Drive. It always fails with drive.ErrOffline.
func (d *Drive) ReadShard(context.Context, ids.ShardID, int64, int64) (io.ReadCloser, error) {
	return nil, drive.ErrOffline
}

// StatShard implements drive.Drive. It always fails with drive.ErrOffline.
func (d *Drive) StatShard(context.Context, ids.ShardID) (drive.ShardInfo, error) {
	return drive.ShardInfo{}, drive.ErrOffline
}

// DeleteShard implements drive.Drive. It always fails with drive.ErrOffline.
func (d *Drive) DeleteShard(context.Context, ids.ShardID) error {
	return drive.ErrOffline
}

// WalkShards implements drive.Drive. It never calls fn and always fails with
// drive.ErrOffline.
func (d *Drive) WalkShards(context.Context, func(drive.ShardInfo) error) error {
	return drive.ErrOffline
}

// Info implements drive.Drive. It reports the identity and location of the
// drive with Online false and no capacity. The error is always nil, so callers
// can read the placeholder without a failure to handle.
func (d *Drive) Info(context.Context) (drive.Info, error) {
	return drive.Info{ID: d.id, Node: d.node, Endpoint: d.endpoint, Online: false}, nil
}

// Close implements drive.Drive. There is nothing to release, so it returns nil.
func (d *Drive) Close() error { return nil }

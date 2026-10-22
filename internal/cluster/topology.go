package cluster

import (
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

// Topology is the node's view of the cluster.
type Topology interface {
	// LocalNode returns this node's id.
	LocalNode() ids.NodeID
	// Map returns the current cluster map. The result must not be modified.
	Map() *Map
	// Drive returns a drive.Drive for id: the local engine for local drives,
	// a UniWire client for remote ones.
	Drive(id ids.DriveID) (drive.Drive, error)
	// Healthy reports whether a node currently answers health checks.
	Healthy(node ids.NodeID) bool
}

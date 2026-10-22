package cluster

import (
	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/ids"
)

// RaftNetwork provides one raft.Transport per Raft group, multiplexed over
// UniWire. Raft server ids and addresses are NodeIDs.
type RaftNetwork interface {
	Transport(group ids.PartitionID) (raft.Transport, error)
}

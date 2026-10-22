package raftstore

import "github.com/eukalpia/unio/internal/ids"

// LeaderForTest returns the leader of partition p as this store knows it. It
// returns false when the store has no replica of p or knows no leader.
func (s *Store) LeaderForTest(p ids.PartitionID) (ids.NodeID, bool) {
	s.mu.RLock()
	g := s.groups[p]
	s.mu.RUnlock()
	if g == nil {
		return "", false
	}
	return g.Leader()
}

// CloseWithoutTransferForTest closes the store the way a crash does: the
// groups stop without a leadership handover. Tests use it to simulate kills.
func (s *Store) CloseWithoutTransferForTest() error {
	return s.closeWith(false)
}

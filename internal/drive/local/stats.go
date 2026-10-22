package local

import (
	"sort"
	"sync"

	"github.com/eukalpia/unio/internal/drive/local/compact"
)

// segStat is the drive's view of one segment: its committed end, the bytes of
// records the index still references, and whether it is sealed.
type segStat struct {
	end    int64
	live   int64
	sealed bool
}

// segStats holds the per-segment accounting. The committer updates it and
// compaction reads it, so it has its own lock.
type segStats struct {
	mu sync.Mutex
	m  map[uint64]*segStat
}

func newSegStats() *segStats {
	return &segStats{m: make(map[uint64]*segStat)}
}

// set replaces the stats of segment id.
func (s *segStats) set(id uint64, st segStat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = &st
}

// apply adds the live-byte deltas of a committed group and records the
// committed end of the active segment.
func (s *segStats) apply(deltas map[uint64]int64, activeID uint64, activeEnd int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, d := range deltas {
		st := s.m[id]
		if st == nil {
			st = &segStat{sealed: id != activeID}
			s.m[id] = st
		}
		st.live += d
	}
	if st := s.m[activeID]; st != nil {
		st.end = activeEnd
	}
}

// seal marks segment id sealed at end.
func (s *segStats) seal(id uint64, end int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.m[id]; st != nil {
		st.sealed = true
		st.end = end
	}
}

// remove forgets segment id.
func (s *segStats) remove(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

// live returns the live bytes of segment id and whether it is known.
func (s *segStats) liveOf(id uint64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[id]
	if !ok {
		return 0, false
	}
	return st.live, true
}

// sealed returns the sealed segments in ascending id order.
func (s *segStats) sealedSegments() []compact.Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]compact.Segment, 0, len(s.m))
	for id, st := range s.m {
		if st.sealed {
			out = append(out, compact.Segment{ID: id, End: st.end, Live: st.live})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// hasSegmentBefore reports whether a segment with an id below id exists.
func (s *segStats) hasSegmentBefore(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for other := range s.m {
		if other < id {
			return true
		}
	}
	return false
}

// segEnd returns the committed end of segment id, or false if it is unknown.
func (s *segStats) segEnd(id uint64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[id]
	if !ok {
		return 0, false
	}
	return st.end, true
}

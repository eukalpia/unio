package local

import (
	"time"

	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// clampedNano returns t as Unix nanoseconds. The index stores creation times as
// unsigned values, so a time before 1970 is recorded as zero.
func clampedNano(t time.Time) int64 {
	if n := t.UnixNano(); n >= 0 {
		return n
	}
	return 0
}

// recordFits reports whether the packed record at loc lies entirely below end,
// the committed end of its segment. The lengths are compared before any sum is
// formed, so no value read from a corrupt index can overflow.
func recordFits(loc index.Location, end int64) bool {
	if loc.Offset < 0 || loc.Length < 0 || loc.Offset > end || loc.Length > end {
		return false
	}
	return segment.RecordSize(loc.Length) <= end-loc.Offset
}

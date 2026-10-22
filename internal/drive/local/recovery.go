package local

import (
	"log/slog"

	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// maxLoggedRanges bounds how many damaged ranges one log line lists.
const maxLoggedRanges = 8

// Recovery describes what Open did to the drive's index.
type Recovery struct {
	// Rebuilt reports that the index was rebuilt from the segment files and
	// the blob directory.
	Rebuilt bool
	// Damaged lists, per segment, the byte ranges that a rebuild skipped
	// because no valid record starts in them. Records on both sides of a
	// range were still recovered.
	Damaged map[uint64][]segment.Range
}

// Recovery returns what Open did. A drive whose index was trusted reports no
// rebuild and no damage.
func (d *Drive) Recovery() Recovery {
	return d.recovery
}

// logDamage reports the damaged ranges of one segment. It logs the count, the
// total number of bytes, and the first few ranges.
func logDamage(root string, seg uint64, ranges []segment.Range) {
	var bytes int64
	for _, r := range ranges {
		bytes += r.End - r.Start
	}
	listed := ranges
	if len(listed) > maxLoggedRanges {
		listed = listed[:maxLoggedRanges]
	}
	slog.Default().Warn("index rebuild skipped damaged byte ranges",
		"drive", root,
		"segment", seg,
		"ranges", len(ranges),
		"bytes", bytes,
		"first", listed)
}

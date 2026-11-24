package segment

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// preallocate reserves size bytes for f so that appends do not allocate.
// Filesystems without fallocate support keep the file growing on demand.
func preallocate(f *os.File, size int64) error {
	err := unix.Fallocate(int(f.Fd()), 0, 0, size)
	switch {
	case err == nil, errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.ENOSYS):
		return nil
	case errors.Is(err, unix.ENOSPC):
		// A nearly full drive cannot reserve a whole segment. Compaction
		// still needs a segment to move live records into before it can
		// free the dead ones, and without one the drive would stay full for
		// good, even if most of it is garbage. So the segment is created
		// without the reservation and grows as records are appended; an
		// append that does not fit fails like any other write and is rolled
		// back. Truncate drops whatever the failed call did allocate.
		return f.Truncate(0)
	}
	return err
}

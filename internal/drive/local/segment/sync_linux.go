package segment

import (
	"os"

	"golang.org/x/sys/unix"
)

// syncData flushes file data and the metadata needed to read it back. On
// Linux this is fdatasync(2), which skips timestamp-only metadata updates.
func syncData(f *os.File) error {
	return unix.Fdatasync(int(f.Fd()))
}

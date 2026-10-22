package segment

import (
	"os"

	"golang.org/x/sys/unix"
)

// syncData flushes file data to the drive's permanent storage. fsync(2) on
// macOS does not flush the drive cache, so F_FULLFSYNC is used when the
// filesystem supports it.
func syncData(f *os.File) error {
	if _, err := unix.FcntlInt(f.Fd(), unix.F_FULLFSYNC, 0); err == nil {
		return nil
	}
	return f.Sync()
}

//go:build !windows

package local

import (
	"golang.org/x/sys/unix"

	"github.com/eukalpia/unio/internal/drive/local/internal/safeint"
)

// capacity returns the total and the available bytes of the filesystem that
// holds path.
func capacity(path string) (total, free uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	// A block size is never negative. A negative value is treated as zero rather
	// than wrapped into a huge size.
	bsize, ok := safeint.ToUint64(int64(st.Bsize))
	if !ok {
		bsize = 0
	}
	return st.Blocks * bsize, st.Bavail * bsize, nil
}

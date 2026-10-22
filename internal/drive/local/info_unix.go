//go:build !windows

package local

import "golang.org/x/sys/unix"

// capacity returns the total and the available bytes of the filesystem that
// holds path.
func capacity(path string) (total, free uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	return st.Blocks * bsize, st.Bavail * bsize, nil
}

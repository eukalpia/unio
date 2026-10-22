//go:build !linux && !darwin

package segment

import "os"

// syncData flushes file data and metadata to stable storage.
func syncData(f *os.File) error {
	return f.Sync()
}

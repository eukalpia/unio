//go:build !linux

package segment

import "os"

// preallocate is a no-op on platforms without fallocate.
func preallocate(_ *os.File, _ int64) error {
	return nil
}

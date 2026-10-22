//go:build windows

package blob

// syncDir is a no-op on Windows, which does not support fsync on directory
// handles. NTFS journals directory metadata.
func syncDir(string) error {
	return nil
}

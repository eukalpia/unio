//go:build !windows

package local

import "os"

// syncDir makes directory entry changes in dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- directory is the drive root joined with a fixed name or a hex id, never user input
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

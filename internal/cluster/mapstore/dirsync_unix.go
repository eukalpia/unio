//go:build !windows

package mapstore

import "os"

// syncDir makes directory entry changes in dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- dir is the metadata directory, never user input
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

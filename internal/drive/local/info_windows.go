//go:build windows

package local

import "golang.org/x/sys/windows"

// capacity returns the total and the available bytes of the volume that holds
// path.
func capacity(path string) (total, free uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var freeToCaller, all, allFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &all, &allFree); err != nil {
		return 0, 0, err
	}
	return all, freeToCaller, nil
}

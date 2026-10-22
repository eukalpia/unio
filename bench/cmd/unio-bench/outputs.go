package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// checkOutputPaths rejects a JSON report and a Markdown report that would be
// written to the same file. Otherwise the second write silently replaces the
// first. Paths are compared after cleaning and making them absolute, and on
// Windows, where file names are case-insensitive, without regard to case. Two
// names for one existing file, such as a hard link, are also rejected. An empty
// path means that report is not written to a file.
func checkOutputPaths(out, markdown string) error {
	if out == "" || markdown == "" {
		return nil
	}
	a, err := absPath(out)
	if err != nil {
		return err
	}
	b, err := absPath(markdown)
	if err != nil {
		return err
	}
	if a == b || (runtime.GOOS == "windows" && strings.EqualFold(a, b)) || sameFile(out, markdown) {
		return fmt.Errorf("-out and -markdown must be different files, but both name %s", a)
	}
	return nil
}

// absPath returns the cleaned absolute form of p.
func absPath(p string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p, err)
	}
	return abs, nil
}

// sameFile reports whether both paths name one existing file. A missing file
// is not the same file as anything.
func sameFile(a, b string) bool {
	infoA, err := os.Stat(a)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}

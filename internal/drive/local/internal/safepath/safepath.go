// Package safepath builds the paths of drive files. A drive opens only files
// below its root, under names it generates itself: fixed names such as
// "segments", and hex encodings of ids. No user input reaches a path.
//
// Join enforces that. It accepts only plain components and refuses any result
// that leaves the root. The layout of a drive and the blob store both build
// their paths through it, so every open in the engine receives a path that has
// been checked this way.
package safepath

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Join returns root joined with parts, cleaned. Each part must be a plain name:
// letters, digits, '.', '_' or '-', and not "." or "..". Join fails for any
// other part, and for a result outside root.
func Join(root string, parts ...string) (string, error) {
	base := filepath.Clean(root)
	for _, p := range parts {
		if !plainName(p) {
			return "", fmt.Errorf("safepath: %q is not a plain file name", p)
		}
	}
	joined := filepath.Join(append([]string{base}, parts...)...)
	if !strings.HasPrefix(joined, base+string(filepath.Separator)) {
		return "", fmt.Errorf("safepath: %q lies outside %q", joined, base)
	}
	return joined, nil
}

// plainName reports whether s is a single path element made only of the
// characters the engine uses in its names.
func plainName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

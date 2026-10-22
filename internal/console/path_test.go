package console

import (
	"io/fs"
	"strings"
	"testing"
)

// TestAssetNameKnownPaths pins the mapping for the paths the console uses.
func TestAssetNameKnownPaths(t *testing.T) {
	tests := []struct {
		path string
		name string
		ok   bool
	}{
		{"/unio/console", "", true},
		{"/unio/console/", "", true},
		{"/unio/console/assets/app-1.js", "assets/app-1.js", true},
		{"/unio/console/assets/app%20x.js", "assets/app x.js", true},
		{"/unio/console/a%2Fb", "a/b", true},
		{"/unio/console/a+b", "a+b", true},
		{"/unio/console/a/", "a", true},
		{"/unio/console/.", "", true},
		{"/unio/console/./", "", true},
		{"/unio/console/a/./b", "", false},
		{"/unio/console/../x", "", false},
		{"/unio/console/%2e%2e/x", "", false},
		{"/unio/console/a%5c", "", false},
		{"/unio/console/a%25", "", false},
		{"/unio/consoleX", "", false},
		{"/other/unio/console/x", "", false},
	}
	for _, tc := range tests {
		name, ok := assetName(tc.path)
		if name != tc.name || ok != tc.ok {
			t.Errorf("assetName(%q) = (%q, %v), want (%q, %v)", tc.path, name, ok, tc.name, tc.ok)
		}
	}
}

// FuzzAssetName checks the invariants that keep requests inside the asset
// tree: a name that is returned is a valid tree path with no escape sequence
// and no backslash, and the function never panics.
func FuzzAssetName(f *testing.F) {
	for _, s := range []string{
		"/unio/console/",
		"/unio/console/../../etc/passwd",
		"/unio/console/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/unio/console/%252e%252e/",
		"/unio/console/assets/index-1.js",
		"/unio/console/a\\..\\b",
		"/unio/console/%",
		"/unio/console/%00",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		name, ok := assetName(raw)
		if !ok {
			return
		}
		if name == "" {
			return
		}
		if !fs.ValidPath(name) {
			t.Fatalf("assetName(%q) returned invalid path %q", raw, name)
		}
		if strings.ContainsAny(name, "\\%") {
			t.Fatalf("assetName(%q) returned %q with a forbidden byte", raw, name)
		}
		for _, elem := range strings.Split(name, "/") {
			if elem == ".." || elem == "." {
				t.Fatalf("assetName(%q) returned %q with a dot element", raw, name)
			}
		}
	})
}

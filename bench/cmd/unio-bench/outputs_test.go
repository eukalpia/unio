package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCheckOutputPaths checks that the JSON and Markdown reports cannot be
// written to the same file. Otherwise the Markdown write would silently replace
// the JSON report.
func TestCheckOutputPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		out     string
		md      string
		wantErr bool
	}{
		{name: "distinct files", out: "a.json", md: "b.md"},
		{name: "markdown not requested", out: "a.json"},
		{name: "json to stdout", md: "b.md"},
		{name: "nothing requested"},
		{name: "same path", out: "r.json", md: "r.json", wantErr: true},
		{name: "spelled with a detour", out: "dir/../r.json", md: "./r.json", wantErr: true},
		{name: "redundant separators", out: "dir//r.json", md: "dir/r.json", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkOutputPaths(tc.out, tc.md)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkOutputPaths(%q, %q) error = %v, wantErr %v", tc.out, tc.md, err, tc.wantErr)
			}
		})
	}
}

// TestCheckOutputPathsRejectsHardLinks checks that two names for one file are
// caught even though their paths differ.
func TestCheckOutputPathsRejectsHardLinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	alias := filepath.Join(dir, "alias.md")
	if err := os.WriteFile(report, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Link(report, alias); err != nil {
		t.Skipf("hard links are not available here: %v", err)
	}
	if err := checkOutputPaths(report, alias); err == nil {
		t.Fatal("a hard link to the JSON report was accepted as a Markdown path")
	}
}

// TestCheckOutputPathsCase checks the case rule. Windows file names are
// case-insensitive, so R.json and r.json are one file there. Elsewhere they are
// two files.
func TestCheckOutputPathsCase(t *testing.T) {
	t.Parallel()
	err := checkOutputPaths("R.json", "r.json")
	if runtime.GOOS == "windows" && err == nil {
		t.Fatal("on Windows, R.json and r.json name one file and must be rejected")
	}
	if runtime.GOOS != "windows" && err != nil {
		t.Fatalf("on this platform R.json and r.json are different files: %v", err)
	}
}

// TestParseConfigRejectsCollidingOutputs checks that the collision is caught
// during argument validation, before any workload runs.
func TestParseConfigRejectsCollidingOutputs(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	_, err := parseConfig([]string{"-endpoint", testEndpoint, "-out", "r.json", "-markdown", "./r.json"}, &stderr)
	if err == nil {
		t.Fatal("parseConfig accepted -out and -markdown naming the same file")
	}
}

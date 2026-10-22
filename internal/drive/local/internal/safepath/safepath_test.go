package safepath

import (
	"path/filepath"
	"testing"
)

func TestJoinAcceptsEngineNames(t *testing.T) {
	root := filepath.Join("data", "drive")
	cases := [][]string{
		{".unio"},
		{".unio", "segments", "0000000000000001.seg"},
		{"blobs", "3f", "3fa0000000000000000000000000000000000001"},
		{"tmp", "0123abcd.tmp"},
		{"index.rebuild"},
	}
	for _, parts := range cases {
		got, err := Join(root, parts...)
		if err != nil {
			t.Fatalf("Join(%v): %v", parts, err)
		}
		want := filepath.Join(append([]string{root}, parts...)...)
		if got != want {
			t.Fatalf("Join(%v) = %q, want %q", parts, got, want)
		}
	}
}

func TestJoinRefusesEscapesAndOddNames(t *testing.T) {
	root := filepath.Join("data", "drive")
	for _, part := range []string{"", ".", "..", "../x", "a/b", `a\b`, "a b", "x\x00y", "/etc"} {
		if got, err := Join(root, part); err == nil {
			t.Fatalf("Join accepted %q as %q", part, got)
		}
	}
}

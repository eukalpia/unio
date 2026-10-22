package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestCommandDoesNotLinkNetHTTPPprof checks the dependency graph of the CLI.
// Linking net/http/pprof registers unauthenticated profiling routes on
// http.DefaultServeMux during init, so the binary must not contain it.
func TestCommandDoesNotLinkNetHTTPPprof(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is not on PATH: %v", err)
	}
	out, err := exec.CommandContext(ctx, goBin, "list", "-deps", "github.com/eukalpia/unio/cmd/unio").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Fields(string(out))
	if len(deps) < 10 {
		t.Fatalf("go list -deps returned %d packages; the check would be meaningless", len(deps))
	}
	for _, dep := range deps {
		if dep == "net/http/pprof" {
			t.Fatal("the CLI links net/http/pprof, which registers routes on http.DefaultServeMux")
		}
	}
}

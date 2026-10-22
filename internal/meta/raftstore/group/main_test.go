package group_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves goroutines running. Shutting
// down a group must stop its raft goroutines and its leadership verifier.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

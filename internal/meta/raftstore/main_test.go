package raftstore_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves goroutines running. Stores,
// raft groups and pebble databases must all be stopped by their Close calls.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

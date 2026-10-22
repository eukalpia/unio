package logstore

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a Pebble or Raft goroutine outlives its test.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

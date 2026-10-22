package metatest

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a goroutine outlives its test, which catches
// leaked transports and handlers in the cluster fakes.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

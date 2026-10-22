package integration

import (
	"fmt"
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs the suite, closes the shared connection pool and then checks
// that the package left no goroutines behind. The check only runs when the
// suite passed, so it does not hide the first failure.
func TestMain(m *testing.M) {
	code := m.Run()
	transport.CloseIdleConnections()
	if code == 0 {
		if err := goleak.Find(); err != nil {
			fmt.Fprintf(os.Stderr, "integration: goroutines left running: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

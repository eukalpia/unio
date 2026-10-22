package obs

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// newTestMetrics returns a fresh Metrics and fails the test if it cannot be built.
func newTestMetrics(t *testing.T) *Metrics {
	t.Helper()
	m, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return m
}

func TestNewMetricsReturnsIndependentInstances(t *testing.T) {
	t.Parallel()
	a := newTestMetrics(t)
	b := newTestMetrics(t)
	if a.Registry() == b.Registry() {
		t.Fatal("instances share a registry")
	}
}

// TestRegisterAllReportsErrors checks that registration failures are returned,
// not panicked. NewMetrics relies on this path.
func TestRegisterAllReportsErrors(t *testing.T) {
	t.Parallel()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "unio_test_duplicate_total", Help: "test"})
	if err := registerAll(prometheus.NewRegistry(), c, c); err == nil {
		t.Fatal("registering the same collector twice returned no error")
	}
	if err := registerAll(prometheus.NewRegistry(), c); err != nil {
		t.Fatalf("registering once: %v", err)
	}
}

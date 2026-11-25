package config

import (
	"testing"
	"time"
)

// TestDefaultsMatchContract pins the defaults that other components rely on.
func TestDefaultsMatchContract(t *testing.T) {
	d := Defaults()
	if d.Node.Region != "us-east-1" {
		t.Errorf("node.region default = %q, want us-east-1", d.Node.Region)
	}
	if d.Timeouts.ReadIdle != 60*time.Second {
		t.Errorf("timeouts.read_idle default = %s, want 60s", d.Timeouts.ReadIdle)
	}
	if d.Timeouts.WriteIdle != 60*time.Second {
		t.Errorf("timeouts.write_idle default = %s, want 60s", d.Timeouts.WriteIdle)
	}
	if d.Timeouts.Write != 0 {
		t.Errorf("timeouts.write default = %s, want 0 (no cap)", d.Timeouts.Write)
	}
}

func TestDefaultsForMetricsAndLimits(t *testing.T) {
	d := Defaults()
	if d.Metrics.PprofAddress != "127.0.0.1:9190" {
		t.Errorf("metrics.pprof_address default = %q, want 127.0.0.1:9190", d.Metrics.PprofAddress)
	}
	if d.Metrics.Token.IsSet() || d.Metrics.Pprof {
		t.Errorf("metrics token and pprof must be off by default")
	}
	if d.Limits.RequestQueue != 4096 {
		t.Errorf("limits.request_queue default = %d, want 4096", d.Limits.RequestQueue)
	}
}

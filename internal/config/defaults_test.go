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

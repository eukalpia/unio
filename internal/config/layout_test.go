package config

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// drivesOf returns n local drives of node1 with distinct paths.
func drivesOf(n int) []Endpoint {
	out := make([]Endpoint, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, Endpoint{Scheme: "https", Host: "node1", Port: 9100, Path: fmt.Sprintf("/data%d", i)})
	}
	return out
}

func TestErasureLayout(t *testing.T) {
	tests := []struct {
		name       string
		drives     int
		setDrives  int
		parity     int
		wantSet    int
		wantParity int
		wantErr    string
	}{
		{name: "automatic set and parity for 16 drives", drives: 16, wantSet: 16, wantParity: 4},
		{name: "automatic set for 4 drives", drives: 4, wantSet: 4, wantParity: 2},
		{name: "single drive has no parity", drives: 1, wantSet: 1, wantParity: 0},
		{name: "automatic set halves 18 drives", drives: 18, wantSet: 9, wantParity: 4},
		{name: "explicit set takes automatic parity", drives: 12, setDrives: 6, wantSet: 6, wantParity: 2},
		{name: "explicit parity is kept", drives: 8, setDrives: 8, parity: 3, wantSet: 8, wantParity: 3},
		{name: "no automatic set for 17 drives", drives: 17, wantErr: "cannot form erasure sets"},
		{name: "set must divide the drives", drives: 12, setDrives: 5, wantErr: "do not divide into erasure sets of 5"},
		{name: "no drives resolved", drives: 0, wantErr: "call Load first"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			c.Cluster.Drives = drivesOf(tt.drives)
			c.Erasure.SetDriveCount = tt.setDrives
			c.Erasure.Parity = tt.parity
			set, parity, err := c.ErasureLayout()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ErasureLayout error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ErasureLayout: %v", err)
			}
			if set != tt.wantSet || parity != tt.wantParity {
				t.Fatalf("ErasureLayout = (%d, %d), want (%d, %d)", set, parity, tt.wantSet, tt.wantParity)
			}
		})
	}
}

func TestMetadataDir(t *testing.T) {
	t.Run("before load there is no drive", func(t *testing.T) {
		c := Defaults()
		if got := c.MetadataDir(); got != "" {
			t.Fatalf("MetadataDir before Load = %q, want empty", got)
		}
	})
	t.Run("default is under the first local drive", func(t *testing.T) {
		cfg, _, err := Load(context.Background(), Options{Env: validEnv()})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.MetadataDir(); got != "/data1/.unio/meta" {
			t.Fatalf("MetadataDir = %q, want /data1/.unio/meta", got)
		}
	})
	t.Run("explicit directory is used as given", func(t *testing.T) {
		env := validEnv()
		env["UNIO_METADATA_DIR"] = "/var/lib/unio/meta"
		cfg, _, err := Load(context.Background(), Options{Env: env})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.MetadataDir(); got != "/var/lib/unio/meta" {
			t.Fatalf("MetadataDir = %q", got)
		}
	})
	t.Run("relative directory is refused", func(t *testing.T) {
		env := validEnv()
		env["UNIO_METADATA_DIR"] = "meta"
		_, _, err := Load(context.Background(), Options{Env: env})
		if err == nil || !strings.Contains(err.Error(), "metadata.dir \"meta\" must be an absolute path") {
			t.Fatalf("Load error = %v, want the absolute path rule", err)
		}
	})
}

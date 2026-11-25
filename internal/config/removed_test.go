package config

import (
	"context"
	"strings"
	"testing"
)

func TestLoadRefusesRemovedSettings(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		flags   map[string]string
		wantErr string
	}{
		{name: "yaml key set to true", yaml: "cluster:\n  insecure_internode: true\n",
			wantErr: "cluster.insecure_internode was removed"},
		{name: "yaml key set to false", yaml: "cluster:\n  insecure_internode: false\n",
			wantErr: "cluster.insecure_internode was removed"},
		{name: "yaml key with no value", yaml: "cluster:\n  insecure_internode:\n",
			wantErr: "cluster.insecure_internode was removed"},
		{name: "environment variable", env: map[string]string{"UNIO_INSECURE_INTERNODE": "true"},
			wantErr: "environment variable UNIO_INSECURE_INTERNODE was removed"},
		{name: "flag", flags: map[string]string{"insecure-internode": "true"},
			wantErr: "flag --insecure-internode was removed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			for k, v := range tt.env {
				env[k] = v
			}
			opts := Options{Env: env, Flags: tt.flags}
			if tt.yaml != "" {
				opts.Path = writeFile(t, "unio.yaml", tt.yaml)
			}
			_, _, err := Load(context.Background(), opts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load error = %v, want substring %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "unknown") {
				t.Fatalf("a removed setting must not get the generic unknown-key error: %v", err)
			}
		})
	}
}

func TestLoadIgnoresEmptyRemovedEnvironmentVariable(t *testing.T) {
	env := validEnv()
	env["UNIO_INSECURE_INTERNODE"] = ""
	if _, _, err := Load(context.Background(), Options{Env: env}); err != nil {
		t.Fatalf("an empty removed variable counts as unset: %v", err)
	}
}

func TestHiddenFlagSpecsAreRemovedSettings(t *testing.T) {
	var hidden []string
	for _, s := range FlagSpecs() {
		if s.Hidden {
			hidden = append(hidden, s.Name)
		}
	}
	if len(hidden) != len(removedSettings()) {
		t.Fatalf("hidden flags = %v, want one per removed setting", hidden)
	}
	for _, name := range hidden {
		if err := checkRemovedFlag(name); err == nil {
			t.Fatalf("hidden flag --%s is not a removed setting", name)
		}
	}
}

func TestLoadNamesRenamedTimeoutRead(t *testing.T) {
	env := validEnv()
	env["UNIO_READ_TIMEOUT"] = "5m"
	_, _, err := Load(context.Background(), Options{Env: env})
	if err == nil || !strings.Contains(err.Error(), "UNIO_READ_TIMEOUT was replaced by timeouts.read_idle") {
		t.Fatalf("Load error = %v, want the replacement named", err)
	}
	path := writeFile(t, "unio.yaml", "timeouts:\n  read: 5m\n")
	_, _, err = Load(context.Background(), Options{Path: path, Env: validEnv()})
	if err == nil || !strings.Contains(err.Error(), "timeouts.read was replaced by timeouts.read_idle") {
		t.Fatalf("Load error = %v, want the replacement named", err)
	}
	_, _, err = Load(context.Background(), Options{Env: validEnv(), Flags: map[string]string{"read-timeout": "5m"}})
	if err == nil || !strings.Contains(err.Error(), "flag --read-timeout was replaced by timeouts.read_idle") {
		t.Fatalf("Load error = %v, want the replacement named", err)
	}
}

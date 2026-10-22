package chaos

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func envFrom(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func baseChaosEnv() map[string]string {
	return map[string]string{
		"UNIO_TEST_ACCESS_KEY": "ak-test",
		"UNIO_TEST_SECRET_KEY": "sk-test",
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(envFrom(baseChaosEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(cfg.ComposeFile) || !strings.HasSuffix(filepath.ToSlash(cfg.ComposeFile), "deploy/compose/cluster-4x4.yml") {
		t.Errorf("ComposeFile = %q, want absolute path to deploy/compose/cluster-4x4.yml", cfg.ComposeFile)
	}
	if cfg.Project != "unio-4x4" {
		t.Errorf("Project = %q", cfg.Project)
	}
	if cfg.Bucket != "unio-chaos" {
		t.Errorf("Bucket = %q", cfg.Bucket)
	}
	if cfg.Duration != 2*time.Minute {
		t.Errorf("Duration = %v", cfg.Duration)
	}
	if cfg.SeedSet {
		t.Errorf("SeedSet = true without UNIO_CHAOS_SEED")
	}
	want := []string{"http://localhost:9001", "http://localhost:9002", "http://localhost:9003", "http://localhost:9004"}
	if diff := cmp.Diff(want, cfg.Endpoints); diff != "" {
		t.Errorf("Endpoints mismatch (-want +got):\n%s", diff)
	}
	if cfg.AccessKey != "ak-test" || cfg.SecretKey != "sk-test" {
		t.Errorf("credentials not read from env")
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	composeFile := filepath.Join(t.TempDir(), "cluster.yml")
	env := baseChaosEnv()
	env["UNIO_CHAOS_COMPOSE_FILE"] = composeFile
	env["UNIO_CHAOS_PROJECT"] = "chaos-1"
	env["UNIO_CHAOS_ENDPOINTS"] = "http://h1:9000/, https://h2:9443"
	env["UNIO_CHAOS_BUCKET"] = "chaos-bucket"
	env["UNIO_CHAOS_DURATION"] = "30s"
	env["UNIO_CHAOS_SEED"] = "42"

	cfg, err := LoadConfig(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComposeFile != composeFile {
		t.Errorf("ComposeFile = %q, want %q", cfg.ComposeFile, composeFile)
	}
	if cfg.Project != "chaos-1" || cfg.Bucket != "chaos-bucket" {
		t.Errorf("Project=%q Bucket=%q", cfg.Project, cfg.Bucket)
	}
	if cfg.Duration != 30*time.Second {
		t.Errorf("Duration = %v", cfg.Duration)
	}
	if !cfg.SeedSet || cfg.Seed != 42 {
		t.Errorf("Seed = %d SeedSet = %v", cfg.Seed, cfg.SeedSet)
	}
	want := []string{"http://h1:9000", "https://h2:9443"}
	if diff := cmp.Diff(want, cfg.Endpoints); diff != "" {
		t.Errorf("Endpoints mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadConfigRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"project starts with dash", "UNIO_CHAOS_PROJECT", "-evil"},
		{"project uppercase", "UNIO_CHAOS_PROJECT", "Unio"},
		{"endpoint scheme", "UNIO_CHAOS_ENDPOINTS", "ftp://h:21"},
		{"endpoint without host", "UNIO_CHAOS_ENDPOINTS", "http://"},
		{"endpoint with path", "UNIO_CHAOS_ENDPOINTS", "http://h:9/prefix"},
		{"empty endpoint entry", "UNIO_CHAOS_ENDPOINTS", "http://h:9,,http://h:10"},
		{"blank endpoint list", "UNIO_CHAOS_ENDPOINTS", " , "},
		{"bucket too short", "UNIO_CHAOS_BUCKET", "ab"},
		{"bucket uppercase", "UNIO_CHAOS_BUCKET", "Chaos"},
		{"duration zero", "UNIO_CHAOS_DURATION", "0s"},
		{"duration not a duration", "UNIO_CHAOS_DURATION", "soon"},
		{"seed negative", "UNIO_CHAOS_SEED", "-1"},
		{"seed not a number", "UNIO_CHAOS_SEED", "abc"},
		{"missing access key", "UNIO_TEST_ACCESS_KEY", ""},
		{"missing secret key", "UNIO_TEST_SECRET_KEY", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := baseChaosEnv()
			env[tt.key] = tt.value
			if _, err := LoadConfig(envFrom(env)); err == nil {
				t.Fatalf("%s=%q accepted", tt.key, tt.value)
			}
		})
	}
}

func TestLoadConfigErrorDoesNotEchoSecret(t *testing.T) {
	env := baseChaosEnv()
	env["UNIO_TEST_ACCESS_KEY"] = ""
	_, err := LoadConfig(envFrom(env))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "sk-test") {
		t.Fatalf("error leaks credential: %v", err)
	}
}

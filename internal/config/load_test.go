package config

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLoadPrecedence(t *testing.T) {
	// The key under test is the S3 listen address, which every source can set.
	// Each row adds one more source, so the result shows which one wins.
	const yamlAddr = ":9101"
	const envAddr = ":9102"
	const flagAddr = ":9103"

	tests := []struct {
		name     string
		yaml     string
		env      map[string]string
		flags    map[string]string
		wantAddr string
	}{
		{name: "defaults only", wantAddr: ":9000"},
		{name: "yaml overrides default", yaml: "node:\n  s3_address: " + yamlAddr + "\n", wantAddr: yamlAddr},
		{name: "env overrides yaml", yaml: "node:\n  s3_address: " + yamlAddr + "\n",
			env: map[string]string{"UNIO_S3_ADDRESS": envAddr}, wantAddr: envAddr},
		{name: "flag overrides env", yaml: "node:\n  s3_address: " + yamlAddr + "\n",
			env: map[string]string{"UNIO_S3_ADDRESS": envAddr}, flags: map[string]string{"s3-address": flagAddr}, wantAddr: flagAddr},
		{name: "empty env does not override yaml", yaml: "node:\n  s3_address: " + yamlAddr + "\n",
			env: map[string]string{"UNIO_S3_ADDRESS": ""}, wantAddr: yamlAddr},
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
			cfg, _, err := Load(context.Background(), opts)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Node.S3Address != tt.wantAddr {
				t.Fatalf("S3Address = %q, want %q", cfg.Node.S3Address, tt.wantAddr)
			}
		})
	}
}

func TestLoadResolvesDrives(t *testing.T) {
	cfg, _, err := Load(context.Background(), Options{Env: validEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Cluster.Drives) != 16 {
		t.Fatalf("drives = %d, want 16", len(cfg.Cluster.Drives))
	}
	local := cfg.LocalDrives()
	if len(local) != 4 {
		t.Fatalf("local drives = %d, want 4", len(local))
	}
	for _, d := range local {
		if d.Host != "node1" {
			t.Fatalf("local drive %s does not belong to node1", d)
		}
	}
}

func TestLoadFromYAMLFileAndEnvironmentPath(t *testing.T) {
	yamlText := `
node:
  id: node1
timeouts:
  read: 2m
log:
  level: debug
`
	path := writeFile(t, "unio.yaml", yamlText)
	env := validEnv()
	delete(env, "UNIO_NODE_ID")

	t.Run("flag path", func(t *testing.T) {
		cfg, _, err := Load(context.Background(), Options{Path: path, Env: env})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Node.ID != "node1" || cfg.Timeouts.Read != 2*time.Minute || cfg.Log.Level != "debug" {
			t.Fatalf("yaml values not applied: %+v", cfg)
		}
	})
	t.Run("UNIO_CONFIG path", func(t *testing.T) {
		withPath := validEnv()
		withPath["UNIO_CONFIG"] = path
		cfg, _, err := Load(context.Background(), Options{Env: withPath})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Timeouts.Read != 2*time.Minute {
			t.Fatalf("read timeout = %s", cfg.Timeouts.Read)
		}
	})
}

func TestLoadYAMLErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "unknown key", content: "node:\n  id: node1\n  bogus: 1\n", wantErr: "bogus"},
		{name: "unknown section", content: "nodes:\n  id: node1\n", wantErr: "nodes"},
		{name: "secret in file", content: "cluster:\n  secret: " + testSecret + "\n", wantErr: "must not appear in the config file"},
		{name: "root password in file", content: "auth:\n  root_password: " + testRootPassword + "\n", wantErr: "must not appear in the config file"},
		{name: "two documents", content: "node:\n  id: a\n---\nnode:\n  id: b\n", wantErr: "exactly one YAML document"},
		{name: "syntax error", content: "node: [\n", wantErr: "config file"},
		{name: "wrong type", content: "node:\n  id: [1, 2]\n", wantErr: "config file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, "unio.yaml", tt.content)
			_, _, err := Load(context.Background(), Options{Path: path, Env: validEnv()})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load error = %v, want substring %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), testSecret) || strings.Contains(err.Error(), testRootPassword) {
				t.Fatalf("error leaks a secret: %v", err)
			}
		})
	}
}

func TestLoadEmptyYAMLFileIsValid(t *testing.T) {
	path := writeFile(t, "empty.yaml", "")
	if _, _, err := Load(context.Background(), Options{Path: path, Env: validEnv()}); err != nil {
		t.Fatalf("empty file: %v", err)
	}
}

func TestLoadMissingYAMLFile(t *testing.T) {
	_, _, err := Load(context.Background(), Options{Path: t.TempDir() + "/missing.yaml", Env: validEnv()})
	if err == nil || !strings.Contains(err.Error(), "cannot open config file") {
		t.Fatalf("missing file error = %v", err)
	}
}

func TestLoadSecretsFromFiles(t *testing.T) {
	secretFile := writeFile(t, "cluster_secret", testSecret+"\n")
	env := validEnv()
	delete(env, "UNIO_CLUSTER_SECRET")
	env["UNIO_CLUSTER_SECRET_FILE"] = secretFile

	cfg, _, err := Load(context.Background(), Options{Env: env})
	if err != nil {
		t.Fatalf("Load with _FILE secret: %v", err)
	}
	if cfg.Cluster.Secret.Expose() != testSecret {
		t.Fatalf("secret from file = %q, want the trailing newline removed", cfg.Cluster.Secret.Expose())
	}
}

func TestLoadSecretSourceErrors(t *testing.T) {
	big := strings.Repeat("x", maxSecretFileSize+1)
	tests := []struct {
		name    string
		mutate  func(env map[string]string)
		wantErr string
	}{
		{name: "both direct and file", mutate: func(env map[string]string) {
			env["UNIO_CLUSTER_SECRET_FILE"] = writeFile(t, "s", testSecret)
		}, wantErr: "both set"},
		{name: "missing file", mutate: func(env map[string]string) {
			delete(env, "UNIO_CLUSTER_SECRET")
			env["UNIO_CLUSTER_SECRET_FILE"] = t.TempDir() + "/nope"
		}, wantErr: "cannot open secret file"},
		{name: "directory instead of file", mutate: func(env map[string]string) {
			delete(env, "UNIO_CLUSTER_SECRET")
			env["UNIO_CLUSTER_SECRET_FILE"] = t.TempDir()
		}, wantErr: "not a regular file"},
		{name: "file too large", mutate: func(env map[string]string) {
			delete(env, "UNIO_CLUSTER_SECRET")
			env["UNIO_CLUSTER_SECRET_FILE"] = writeFile(t, "big", big)
		}, wantErr: "larger than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			tt.mutate(env)
			_, _, err := Load(context.Background(), Options{Env: env})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadRejectsSecretFlags(t *testing.T) {
	for _, name := range []string{"cluster-secret", "root-password", "root-user", "metrics-token"} {
		_, _, err := Load(context.Background(), Options{Env: validEnv(), Flags: map[string]string{name: "x"}})
		if err == nil || !strings.Contains(err.Error(), "unknown flag --"+name) {
			t.Fatalf("flag --%s must not exist, error = %v", name, err)
		}
	}
}

func TestLoadFlagAndEnvErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		flags   map[string]string
		wantErr string
	}{
		{name: "unknown flag", flags: map[string]string{"no-such-flag": "1"}, wantErr: "unknown flag --no-such-flag"},
		{name: "bad int from flag", flags: map[string]string{"max-requests": "many"}, wantErr: "flag --max-requests"},
		{name: "bad int from env", env: map[string]string{"UNIO_MAX_REQUESTS": "many"}, wantErr: "UNIO_MAX_REQUESTS"},
		{name: "bad bool from env", env: map[string]string{"UNIO_REQUIRE_TLS": "maybe"}, wantErr: "UNIO_REQUIRE_TLS"},
		{name: "bad size from env", env: map[string]string{"UNIO_MEMORY_BUDGET": "1 XB"}, wantErr: "UNIO_MEMORY_BUDGET"},
		{name: "bad duration from env", env: map[string]string{"UNIO_READ_TIMEOUT": "soon"}, wantErr: "UNIO_READ_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			for k, v := range tt.env {
				env[k] = v
			}
			_, _, err := Load(context.Background(), Options{Env: env, Flags: tt.flags})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadWarnsOnUnknownEnvironmentVariables(t *testing.T) {
	env := validEnv()
	env["UNIO_ROOT_PASWORD"] = "typo"
	env["UNRELATED"] = "ignored"
	_, warnings, err := Load(context.Background(), Options{Env: env})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "UNIO_ROOT_PASWORD") {
		t.Fatalf("warnings = %q, want the misspelled variable", joined)
	}
	if strings.Contains(joined, "UNRELATED") {
		t.Fatalf("warnings must ignore non-UNIO variables: %q", joined)
	}
}

func TestLoadReportsPlainHTTPWarning(t *testing.T) {
	_, warnings, err := Load(context.Background(), Options{Env: validEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "plain HTTP") {
		t.Fatalf("warnings = %q, want the plain HTTP warning", warnings)
	}
}

func TestLoadRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Load(ctx, Options{Env: validEnv()}); err == nil {
		t.Fatal("a cancelled context must fail Load")
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	env := validEnv()
	delete(env, "UNIO_ROOT_PASSWORD")
	_, _, err := Load(context.Background(), Options{Env: env})
	if err == nil || !strings.Contains(err.Error(), "auth.root_password is not set") {
		t.Fatalf("Load error = %v, want the missing root password", err)
	}
}

func TestFlagSpecsAreConsistent(t *testing.T) {
	seenFlags := map[string]bool{}
	seenEnv := map[string]bool{}
	for _, s := range FlagSpecs() {
		if seenFlags[s.Name] || seenEnv[s.Env] {
			t.Fatalf("duplicate flag or variable: %+v", s)
		}
		seenFlags[s.Name], seenEnv[s.Env] = true, true
		if !strings.HasPrefix(s.Env, "UNIO_") || s.Usage == "" || strings.HasPrefix(s.Name, "-") {
			t.Fatalf("malformed flag spec: %+v", s)
		}
		if strings.Contains(s.Env, "SECRET") || strings.Contains(s.Env, "PASSWORD") || strings.Contains(s.Env, "TOKEN") {
			t.Fatalf("secret %s must not have a flag", s.Env)
		}
	}
}

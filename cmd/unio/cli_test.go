package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Strong test fixtures. They pass validation and are used only in tests.
const (
	fxSecret   = "Q7vZ2kLm9pR4xT8wN1bC6yH3dF5sG0jE"
	fxUser     = "unioRootAdm01"
	fxPassword = "Pw7Kd2Rx9Lm4Vq8Zt1Nb6Hc3Yf5Gs0Ja"
	fxToken    = "Hm4Tq9Zr2Lx7Wc1Pk8Vb3Ny6Gd5Fs0JaKq"
)

// validEnv returns an environment that loads into a valid four-node configuration for node1.
func validEnv() map[string]string {
	return map[string]string{
		"UNIO_NODE_ID":        "node1",
		"UNIO_ENDPOINTS":      "https://node{1...4}:9100/data{1...4}",
		"UNIO_CLUSTER_SECRET": fxSecret,
		"UNIO_ROOT_USER":      fxUser,
		"UNIO_ROOT_PASSWORD":  fxPassword,
		"UNIO_METRICS_TOKEN":  fxToken,
	}
}

// cliResult is the outcome of one CLI invocation.
type cliResult struct {
	stdout string
	stderr string
	code   int
}

// runCLI runs the CLI in-process with the given environment and arguments.
func runCLI(t *testing.T, env map[string]string, args ...string) cliResult {
	t.Helper()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), args, app{stdout: &out, stderr: &errOut, env: env})
	return cliResult{stdout: out.String(), stderr: errOut.String(), code: code}
}

// withEnv returns a copy of base with the given overrides applied. A nil value removes the key.
func withEnv(base map[string]string, overrides map[string]*string) map[string]string {
	out := make(map[string]string, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		if v == nil {
			delete(out, k)
		} else {
			out[k] = *v
		}
	}
	return out
}

// str returns a pointer to s, for use in withEnv overrides.
func str(s string) *string { return &s }

func TestRootCommandListsSubcommands(t *testing.T) {
	res := runCLI(t, validEnv(), "--help")
	if res.code != 0 {
		t.Fatalf("--help exit code = %d, stderr = %s", res.code, res.stderr)
	}
	for _, sub := range []string{"server", "init", "version", "config", "healthcheck"} {
		if !strings.Contains(res.stdout, sub) {
			t.Errorf("help does not mention %q:\n%s", sub, res.stdout)
		}
	}
}

func TestUnknownCommandFails(t *testing.T) {
	res := runCLI(t, validEnv(), "frobnicate")
	if res.code == 0 {
		t.Fatal("unknown command must fail")
	}
}

func TestServerCommand(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		args       []string
		wantCode   int
		wantStderr string
		notStderr  string
	}{
		{
			name:       "valid configuration reaches the placeholder",
			env:        validEnv(),
			wantCode:   1,
			wantStderr: "not yet available",
		},
		{
			name:       "invalid configuration is reported before starting",
			env:        withEnv(validEnv(), map[string]*string{"UNIO_CLUSTER_SECRET": nil}),
			wantCode:   1,
			wantStderr: "cluster.secret is not set",
			notStderr:  "not yet available",
		},
		{
			name:       "flags override the environment",
			env:        validEnv(),
			args:       []string{"--node-id", "node9"},
			wantCode:   1,
			wantStderr: "no endpoint belongs to node \"node9\"",
		},
		{
			name:       "unknown flag is an error",
			env:        validEnv(),
			args:       []string{"--bogus"},
			wantCode:   1,
			wantStderr: "unknown flag",
		},
		{
			name:       "plain http warning is logged as JSON",
			env:        validEnv(),
			wantCode:   1,
			wantStderr: "plain HTTP",
		},
		{
			name: "insecure internode prints a banner",
			env: withEnv(validEnv(), map[string]*string{
				"UNIO_ENDPOINTS":          str("http://node{1...4}:9100/data{1...4}"),
				"UNIO_INSECURE_INTERNODE": str("true"),
			}),
			wantCode:   1,
			wantStderr: "INSECURE INTERNODE TRAFFIC",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"server"}, tt.args...)
			res := runCLI(t, tt.env, args...)
			if res.code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr:\n%s", res.code, tt.wantCode, res.stderr)
			}
			if !strings.Contains(res.stderr, tt.wantStderr) {
				t.Fatalf("stderr does not contain %q:\n%s", tt.wantStderr, res.stderr)
			}
			if tt.notStderr != "" && strings.Contains(res.stderr, tt.notStderr) {
				t.Fatalf("stderr unexpectedly contains %q:\n%s", tt.notStderr, res.stderr)
			}
			for _, secret := range []string{fxSecret, fxPassword, fxToken} {
				if strings.Contains(res.stderr, secret) || strings.Contains(res.stdout, secret) {
					t.Fatalf("output leaks a secret:\n%s", res.stderr)
				}
			}
		})
	}
}

func TestConfigValidateCommand(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		res := runCLI(t, validEnv(), "config", "validate")
		if res.code != 0 || !strings.Contains(res.stdout, "configuration is valid") {
			t.Fatalf("code = %d, stdout = %q, stderr = %q", res.code, res.stdout, res.stderr)
		}
	})
	t.Run("invalid reports every problem", func(t *testing.T) {
		env := withEnv(validEnv(), map[string]*string{
			"UNIO_NODE_ID":   nil,
			"UNIO_ROOT_USER": str("administrator"),
			"UNIO_LOG_LEVEL": str("loud"),
		})
		res := runCLI(t, env, "config", "validate")
		if res.code != 1 {
			t.Fatalf("exit code = %d, want 1", res.code)
		}
		for _, want := range []string{"node.id is required", "well-known default", "log.level"} {
			if !strings.Contains(res.stderr, want) {
				t.Errorf("stderr does not mention %q:\n%s", want, res.stderr)
			}
		}
	})
	t.Run("warnings are printed and the config is still valid", func(t *testing.T) {
		env := withEnv(validEnv(), map[string]*string{
			"UNIO_ENDPOINTS": str("https://node1:9100/data1"),
		})
		res := runCLI(t, env, "config", "validate")
		if res.code != 0 || !strings.Contains(res.stderr, "single drive") {
			t.Fatalf("code = %d, stderr = %q", res.code, res.stderr)
		}
	})
	t.Run("config file via flag", func(t *testing.T) {
		path := writeTempFile(t, "node:\n  id: node2\n")
		res := runCLI(t, validEnv(), "config", "validate", "--config", path)
		if res.code != 0 {
			t.Fatalf("exit code = %d, stderr = %s", res.code, res.stderr)
		}
	})
}

func TestConfigPrintCommand(t *testing.T) {
	t.Run("redacts secrets and prints the effective values", func(t *testing.T) {
		res := runCLI(t, validEnv(), "config", "print")
		if res.code != 0 {
			t.Fatalf("exit code = %d, stderr = %s", res.code, res.stderr)
		}
		for _, secret := range []string{fxSecret, fxUser, fxPassword, fxToken} {
			if strings.Contains(res.stdout, secret) {
				t.Fatalf("printed configuration leaks a secret:\n%s", res.stdout)
			}
		}
		for _, want := range []string{"[REDACTED]", "id: node1", "https://node{1...4}:9100/data{1...4}", "read_header: 10s"} {
			if !strings.Contains(res.stdout, want) {
				t.Errorf("printed configuration does not contain %q:\n%s", want, res.stdout)
			}
		}
	})
	t.Run("flag overrides are applied", func(t *testing.T) {
		res := runCLI(t, validEnv(), "config", "print", "--s3-address", ":9500")
		if res.code != 0 || !strings.Contains(res.stdout, `s3_address: :9500`) {
			t.Fatalf("code = %d, stdout = %s", res.code, res.stdout)
		}
	})
	t.Run("invalid configuration is not printed", func(t *testing.T) {
		env := withEnv(validEnv(), map[string]*string{"UNIO_METRICS_TOKEN": nil})
		res := runCLI(t, env, "config", "print")
		if res.code != 1 || res.stdout != "" {
			t.Fatalf("code = %d, stdout = %q", res.code, res.stdout)
		}
	})
}

func TestEnvironmentParsing(t *testing.T) {
	got := environment([]string{"A=b=c", "=C:=x", "NOEQUALS", "EMPTY=", "UNIO_NODE_ID=node1"})
	want := map[string]string{"A": "b=c", "EMPTY": "", "UNIO_NODE_ID": "node1"}
	if len(got) != len(want) {
		t.Fatalf("environment() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("environment()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestSignalContextCancelsWithParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, stop := signalContext(parent)
	defer stop()
	if ctx.Err() != nil {
		t.Fatal("signal context must start open")
	}
	cancelParent()
	<-ctx.Done()
}

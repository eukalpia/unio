package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/config"
)

// parseEnvFile reads KEY=value lines, ignoring comments and blank lines.
func parseEnvFile(t *testing.T, data []byte) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed env line %q", line)
		}
		out[key] = value
	}
	return out
}

// writeTempFile writes content to a new file in a temporary directory and returns its path.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unio.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestInitWritesValidCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unio.env")
	res := runCLI(t, validEnv(), "init", "--output", path)
	if res.code != 0 {
		t.Fatalf("init exit code = %d, stderr = %s", res.code, res.stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	env := parseEnvFile(t, data)
	for _, key := range []string{"UNIO_CLUSTER_SECRET", "UNIO_ROOT_USER", "UNIO_ROOT_PASSWORD", "UNIO_METRICS_TOKEN"} {
		if env[key] == "" {
			t.Fatalf("generated file lacks %s", key)
		}
	}
	if err := config.CheckRootUser(env["UNIO_ROOT_USER"]); err != nil {
		t.Errorf("generated root user rejected: %v", err)
	}
	if err := config.CheckRootPassword(env["UNIO_ROOT_USER"], env["UNIO_ROOT_PASSWORD"]); err != nil {
		t.Errorf("generated root password rejected: %v", err)
	}
	if err := config.CheckSecretValue(env["UNIO_CLUSTER_SECRET"]); err != nil {
		t.Errorf("generated cluster secret rejected: %v", err)
	}
	if err := config.CheckSecretValue(env["UNIO_METRICS_TOKEN"]); err != nil {
		t.Errorf("generated metrics token rejected: %v", err)
	}
	if env["UNIO_CLUSTER_SECRET"] == env["UNIO_METRICS_TOKEN"] {
		t.Error("cluster secret and metrics token must differ")
	}

	// The generated file must load into a valid configuration together with the rest of the node's setup.
	merged := validEnv()
	for k, v := range env {
		merged[k] = v
	}
	if _, _, err := config.Load(context.Background(), config.Options{Env: merged}); err != nil {
		t.Fatalf("configuration with generated credentials does not validate: %v", err)
	}
}

func TestInitDoesNotPrintSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unio.env")
	res := runCLI(t, validEnv(), "init", "--output", path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	env := parseEnvFile(t, data)
	for k, v := range env {
		if strings.Contains(res.stdout, v) || strings.Contains(res.stderr, v) {
			t.Fatalf("%s value printed to the console", k)
		}
	}
}

func TestInitFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "unio.env")
	if res := runCLI(t, validEnv(), "init", "--output", path); res.code != 0 {
		t.Fatalf("init failed: %s", res.stderr)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("file mode = %o, want 600", mode)
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unio.env")
	const original = "UNIO_ROOT_USER=keep-me\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	res := runCLI(t, validEnv(), "init", "--output", path)
	if res.code == 0 || !strings.Contains(res.stderr, "refusing to overwrite") {
		t.Fatalf("code = %d, stderr = %q; want an overwrite refusal", res.code, res.stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
}

func TestInitErrors(t *testing.T) {
	t.Run("missing output flag", func(t *testing.T) {
		res := runCLI(t, validEnv(), "init")
		if res.code == 0 || !strings.Contains(res.stderr, "output") {
			t.Fatalf("code = %d, stderr = %q", res.code, res.stderr)
		}
	})
	t.Run("directory does not exist", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "no-such-dir", "unio.env")
		res := runCLI(t, validEnv(), "init", "--output", path)
		if res.code == 0 || !strings.Contains(res.stderr, "cannot create") {
			t.Fatalf("code = %d, stderr = %q", res.code, res.stderr)
		}
	})
}

func TestCreateNewFileRejectsEmptyPath(t *testing.T) {
	if err := createNewFile("", []byte("x")); err == nil {
		t.Fatal("empty path must be rejected")
	}
}

func TestGenerateCredentials(t *testing.T) {
	t.Run("crypto source produces valid credentials", func(t *testing.T) {
		creds, err := generateCredentials(rand.Reader)
		if err != nil {
			t.Fatalf("generateCredentials: %v", err)
		}
		if len(creds.clusterSecret) < config.MinSecretLength || len(creds.rootPassword) < config.MinRootPasswordLength {
			t.Fatalf("generated values are too short")
		}
	})
	t.Run("constant bytes never pass the strength rules", func(t *testing.T) {
		_, err := generateCredentials(zeroReader{})
		if err == nil || !strings.Contains(err.Error(), "could not generate a valid value") {
			t.Fatalf("error = %v, want a generation failure", err)
		}
	})
	t.Run("read failure is reported", func(t *testing.T) {
		_, err := generateCredentials(failingReader{})
		if err == nil || !strings.Contains(err.Error(), "read random bytes") {
			t.Fatalf("error = %v, want a read failure", err)
		}
	})
}

func TestFormatEnvFileIsDeterministicInShape(t *testing.T) {
	creds := credentials{clusterSecret: "a", rootUser: "b", rootPassword: "c", metricsToken: "d"}
	when := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	got := string(formatEnvFile(creds, when))
	if !strings.Contains(got, "2026-10-22T12:00:00Z") {
		t.Fatalf("header lacks the timestamp: %q", got)
	}
	for _, line := range []string{"UNIO_CLUSTER_SECRET=a", "UNIO_ROOT_USER=b", "UNIO_ROOT_PASSWORD=c", "UNIO_METRICS_TOKEN=d"} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line %q in %q", line, got)
		}
	}
	if bytes.Count([]byte(got), []byte("\n")) != 5 {
		t.Errorf("unexpected number of lines in %q", got)
	}
}

// zeroReader yields an endless stream of zero bytes, which never meets the strength rules.
type zeroReader struct{}

// Read fills p with zeros.
func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// failingReader always returns an error.
type failingReader struct{}

// Read always fails.
func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy source unavailable")
}

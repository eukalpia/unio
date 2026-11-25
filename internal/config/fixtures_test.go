package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
)

// Test fixtures. They are strong enough to pass validation and are used only in tests.
const (
	testSecret        = "Q7vZ2kLm9pR4xT8wN1bC6yH3dF5sG0jE"
	testRootUser      = "unioRootAdm01"
	testRootPassword  = "Pw7Kd2Rx9Lm4Vq8Zt1Nb6Hc3Yf5Gs0Ja"
	testMetricsToken  = "Hm4Tq9Zr2Lx7Wc1Pk8Vb3Ny6Gd5Fs0JaKq"
	testEndpointsPatt = "https://node{1...4}:9100/data{1...4}"
)

// validConfig returns a configuration that passes validation with no errors.
func validConfig() Config {
	c := Defaults()
	c.Node.ID = "node1"
	c.Cluster.Endpoints = []string{testEndpointsPatt}
	c.Cluster.Secret = NewSecret(testSecret)
	c.Auth.RootUser = NewSecret(testRootUser)
	c.Auth.RootPassword = NewSecret(testRootPassword)
	c.Metrics.Token = NewSecret(testMetricsToken)
	return c
}

// validEnv returns an environment that loads into a valid configuration.
func validEnv() map[string]string {
	return map[string]string{
		"UNIO_NODE_ID":        "node1",
		"UNIO_ENDPOINTS":      testEndpointsPatt,
		"UNIO_CLUSTER_SECRET": testSecret,
		"UNIO_ROOT_USER":      testRootUser,
		"UNIO_ROOT_PASSWORD":  testRootPassword,
		"UNIO_METRICS_TOKEN":  testMetricsToken,
	}
}

// writeFile writes content to a file in a temporary directory and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestFixturesAreStrong(t *testing.T) {
	if err := CheckSecretValue(testSecret); err != nil {
		t.Fatalf("testSecret: %v", err)
	}
	if err := CheckSecretValue(testMetricsToken); err != nil {
		t.Fatalf("testMetricsToken: %v", err)
	}
	if err := auth.ValidateRootCredentials(testRootUser, testRootPassword); err != nil {
		t.Fatalf("root credentials: %v", err)
	}
	if testSecret == testMetricsToken {
		t.Fatal("fixtures must differ")
	}
}

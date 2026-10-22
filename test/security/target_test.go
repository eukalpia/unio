package security

import (
	"net/url"
	"os"
	"testing"
	"time"
)

// target describes the endpoint under test.
type target struct {
	endpoint  *url.URL
	region    string
	accessKey string
	secretKey string
	// flavor is "unio" (default) or another implementation name. Tests for
	// behaviour that S3 leaves open, and Unio defines strictly, run only
	// against Unio.
	flavor string
	// headerTimeout bounds how long the server may keep a connection whose
	// request headers never complete.
	headerTimeout time.Duration
	// bodyIdleTimeout bounds how long the server may wait for a stalled body.
	bodyIdleTimeout time.Duration
}

// loadTarget reads the target from the environment or skips the test.
func loadTarget(t *testing.T) target {
	t.Helper()
	raw := os.Getenv("UNIO_TEST_ENDPOINT")
	if raw == "" {
		t.Skip("UNIO_TEST_ENDPOINT not set")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		t.Fatalf("UNIO_TEST_ENDPOINT %q is not a URL", raw)
	}
	tg := target{
		endpoint:        u,
		region:          envOr("UNIO_TEST_REGION", "us-east-1"),
		accessKey:       os.Getenv("UNIO_TEST_ACCESS_KEY"),
		secretKey:       os.Getenv("UNIO_TEST_SECRET_KEY"),
		flavor:          envOr("UNIO_TEST_FLAVOR", "unio"),
		headerTimeout:   envDuration(t, "UNIO_TEST_HEADER_TIMEOUT", 30*time.Second),
		bodyIdleTimeout: envDuration(t, "UNIO_TEST_BODY_IDLE_TIMEOUT", 60*time.Second),
	}
	if tg.accessKey == "" || tg.secretKey == "" {
		t.Fatal("UNIO_TEST_ACCESS_KEY and UNIO_TEST_SECRET_KEY must be set")
	}
	return tg
}

// unioOnly skips tests of behaviour that only Unio guarantees.
func (tg target) unioOnly(t *testing.T) {
	t.Helper()
	if tg.flavor != "unio" {
		t.Skipf("Unio-specific guarantee; target flavor is %q", tg.flavor)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(t *testing.T, key string, def time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return d
}

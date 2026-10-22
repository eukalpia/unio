package integration

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Environment variables read by the suite.
const (
	envEndpoint  = "UNIO_TEST_ENDPOINT"
	envAccessKey = "UNIO_TEST_ACCESS_KEY"
	envSecretKey = "UNIO_TEST_SECRET_KEY"
	envRegion    = "UNIO_TEST_REGION"
	// envCAFile optionally names a PEM file with the CA certificate that signs
	// an https endpoint. It is needed only when the CA is not in the system pool.
	envCAFile = "UNIO_TEST_CA_FILE"
)

// defaultRegion is the signing region used when UNIO_TEST_REGION is unset.
const defaultRegion = "us-east-1"

// config is the connection settings for the endpoint under test.
type config struct {
	endpoint  string
	accessKey string
	secretKey string
	region    string
	caFile    string
}

// errNotConfigured reports that UNIO_TEST_ENDPOINT is unset, so the suite is skipped.
var errNotConfigured = errors.New(envEndpoint + " is not set")

// lookupFunc has the shape of os.LookupEnv, so parsing needs no globals in tests.
type lookupFunc func(key string) (string, bool)

// parseConfig reads the suite configuration through lookup. It returns
// errNotConfigured when the endpoint is unset or blank. Error messages name the
// variables involved but never echo credential values.
func parseConfig(lookup lookupFunc) (config, error) {
	raw := lookupTrimmed(lookup, envEndpoint)
	if raw == "" {
		return config{}, errNotConfigured
	}
	endpoint, err := parseEndpoint(raw)
	if err != nil {
		return config{}, err
	}
	access := lookupTrimmed(lookup, envAccessKey)
	secret := lookupTrimmed(lookup, envSecretKey)
	if access == "" || secret == "" {
		return config{}, fmt.Errorf("%s and %s must both be set when %s is set", envAccessKey, envSecretKey, envEndpoint)
	}
	region := lookupTrimmed(lookup, envRegion)
	if region == "" {
		region = defaultRegion
	}
	caFile := lookupTrimmed(lookup, envCAFile)
	if caFile != "" && !strings.HasPrefix(endpoint, "https://") {
		return config{}, fmt.Errorf("%s only applies to an https endpoint", envCAFile)
	}
	return config{endpoint: endpoint, accessKey: access, secretKey: secret, region: region, caFile: caFile}, nil
}

// lookupTrimmed returns the trimmed value of key, or "" when it is unset.
func lookupTrimmed(lookup lookupFunc, key string) string {
	v, _ := lookup(key)
	return strings.TrimSpace(v)
}

// parseEndpoint checks that raw is a bare http or https base URL and returns
// it as scheme://host. It rejects embedded credentials, paths, queries and
// fragments, and it does not echo raw in its errors because it may contain
// credentials.
func parseEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%s is not a valid URL", envEndpoint)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return "", fmt.Errorf("%s must use the http or https scheme", envEndpoint)
	case u.Host == "":
		return "", fmt.Errorf("%s must include a host", envEndpoint)
	case u.User != nil:
		return "", fmt.Errorf("%s must not embed credentials; use %s and %s", envEndpoint, envAccessKey, envSecretKey)
	case u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/"):
		return "", fmt.Errorf("%s must be a bare base URL without a path, query or fragment", envEndpoint)
	}
	return u.Scheme + "://" + u.Host, nil
}

// loadConfig returns the suite configuration from the process environment. It
// skips t when UNIO_TEST_ENDPOINT is unset and fails t when the configuration
// is invalid.
func loadConfig(t *testing.T) config {
	t.Helper()
	cfg, err := parseConfig(os.LookupEnv)
	switch {
	case errors.Is(err, errNotConfigured):
		t.Skipf("%s is not set: skipping the S3 integration test (see test/integration/README.md)", envEndpoint)
	case err != nil:
		t.Fatalf("invalid integration configuration: %v", err)
	}
	return cfg
}

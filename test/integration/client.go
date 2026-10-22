package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// testTimeout bounds the requests of one ordinary test.
const testTimeout = 2 * time.Minute

// transport is the HTTP transport shared by every S3 client in the package and
// by the raw HTTP calls in the presign tests. It starts from the SDK defaults.
// TestMain closes its idle connections before it checks for leaked goroutines.
var transport = awshttp.NewBuildableClient().GetTransport()

// httpClient wraps transport. Requests are bounded by their context, not by a
// client-wide timeout.
var httpClient = &http.Client{Transport: transport}

// newClient returns an S3 client for cfg. It uses path-style addressing and a
// single attempt per request, so each test sees the server's answer and not a
// silent retry of it. The checksum settings are left at the SDK defaults.
func newClient(cfg config) *s3.Client {
	return s3.New(s3.Options{
		Region:           cfg.region,
		Credentials:      credentials.NewStaticCredentialsProvider(cfg.accessKey, cfg.secretKey, ""),
		BaseEndpoint:     aws.String(cfg.endpoint),
		UsePathStyle:     true,
		RetryMaxAttempts: 1,
		HTTPClient:       httpClient,
	})
}

// setup loads the configuration (skipping t when the endpoint is unset) and
// returns a context that expires after testTimeout, along with a client.
func setup(t *testing.T) (context.Context, *s3.Client) {
	t.Helper()
	return setupWithTimeout(t, testTimeout)
}

// setupWithTimeout is setup with a caller-chosen deadline for the test.
func setupWithTimeout(t *testing.T, d time.Duration) (context.Context, *s3.Client) {
	t.Helper()
	cfg := loadConfig(t)
	if cfg.caFile != "" {
		if err := trustCA(cfg.caFile); err != nil {
			t.Fatalf("%v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx, newClient(cfg)
}

var (
	caOnce sync.Once
	caErr  error
)

// trustCA makes the shared transport trust the PEM certificates in path. It
// runs once per process, before any request is sent, because every test calls
// setup first. Later calls return the result of the first.
func trustCA(path string) error {
	caOnce.Do(func() {
		pemData, err := os.ReadFile(path)
		if err != nil {
			caErr = fmt.Errorf("read %s: %w", envCAFile, err)
			return
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			caErr = fmt.Errorf("%s contains no PEM certificates", envCAFile)
			return
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	})
	return caErr
}

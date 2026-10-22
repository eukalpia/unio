package s3api_test

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// discardLogger returns a logger that drops every record.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// startServer serves cfg with the fake layer on a random localhost port and
// returns the base URL, using https when cfg configures TLS. The server stops
// when the test ends, and Serve must return without error.
func startServer(t testing.TB, cfg s3api.Config, authz auth.Authorizer) string {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if authz == nil {
		authz = &fakes.Authorizer{}
	}
	srv, err := s3api.New(cfg, fakes.NewLayer(), &fakes.Authenticator{}, authz, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve returned: %v", err)
		}
	})
	scheme := "http"
	if cfg.TLSCertFile != "" {
		scheme = "https"
	}
	return scheme + "://" + ln.Addr().String()
}

// newHTTPClient returns a client with its own transport, closed at test end.
// tlsCfg, when non-nil, configures trust for a self-signed server certificate.
func newHTTPClient(t testing.TB, tlsCfg *tls.Config) *http.Client {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: tlsCfg}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

// newS3 returns an AWS SDK S3 client for base, with path-style addressing and
// checksums calculated only when the operation requires them.
func newS3(base string, hc *http.Client) *s3.Client {
	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKIAE2ETEST0000000000", "e2e-secret-key-for-tests-only-00000", ""),
		HTTPClient:  hc,
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(base)
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
}

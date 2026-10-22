package s3api

import (
	"crypto/tls"
	"errors"
	"fmt"
	"time"
)

// Default values applied to zero Config fields.
const (
	DefaultReadHeaderTimeout    = 10 * time.Second
	DefaultReadBodyIdleTimeout  = 60 * time.Second
	DefaultWriteTimeout         = time.Hour
	DefaultIdleTimeout          = 2 * time.Minute
	DefaultShutdownTimeout      = 30 * time.Second
	DefaultMaxConcurrentRequest = 256
	DefaultMaxQueuedRequests    = 1024
	DefaultRegion               = "us-east-1"
)

// Config configures the S3 HTTP front end. Zero durations and limits take the
// Default values above; Addr is required.
type Config struct {
	// Addr is the listen address, for example ":9000" or "127.0.0.1:0".
	Addr string
	// TLSCertFile and TLSKeyFile enable HTTPS when both are set. Setting only
	// one is an error.
	TLSCertFile string
	TLSKeyFile  string
	// RequireTLS refuses to start without TLS (--require-tls).
	RequireTLS bool
	// ReadHeaderTimeout bounds how long a client may take to send the
	// request head, including on the first request of a connection.
	ReadHeaderTimeout time.Duration
	// ReadBodyIdleTimeout bounds the wait for each read of a request body, so
	// a slow client cannot hold a connection open while sending nothing.
	ReadBodyIdleTimeout time.Duration
	// WriteTimeout is the absolute limit for writing one response.
	WriteTimeout time.Duration
	// IdleTimeout bounds how long a keep-alive connection may stay idle.
	IdleTimeout time.Duration
	// ShutdownTimeout bounds the graceful drain in Run after ctx is done.
	ShutdownTimeout time.Duration
	// MaxConcurrentRequests is the number of requests served at once.
	MaxConcurrentRequests int
	// MaxQueuedRequests is the number of requests that may wait for a slot;
	// further requests receive 503 SlowDown.
	MaxQueuedRequests int
	// Region is the bucket location reported by GetBucketLocation and the only
	// region accepted in CreateBucket.
	Region string
	// Domains are the DNS suffixes for virtual-hosted-style addressing, for
	// example "s3.unio.example". Empty means path-style only.
	Domains []string
}

// withDefaults returns a copy of c with zero fields replaced by defaults, and
// validates it.
func (c Config) withDefaults() (Config, error) {
	if c.Addr == "" {
		return c, errors.New("s3api: listen address is required")
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return c, errors.New("s3api: TLS certificate and key must be given together")
	}
	if c.RequireTLS && c.TLSCertFile == "" {
		return c, errors.New("s3api: RequireTLS is set but no TLS certificate is configured")
	}
	setDuration(&c.ReadHeaderTimeout, DefaultReadHeaderTimeout)
	setDuration(&c.ReadBodyIdleTimeout, DefaultReadBodyIdleTimeout)
	setDuration(&c.WriteTimeout, DefaultWriteTimeout)
	setDuration(&c.IdleTimeout, DefaultIdleTimeout)
	setDuration(&c.ShutdownTimeout, DefaultShutdownTimeout)
	if c.MaxConcurrentRequests == 0 {
		c.MaxConcurrentRequests = DefaultMaxConcurrentRequest
	}
	if c.MaxQueuedRequests == 0 {
		c.MaxQueuedRequests = DefaultMaxQueuedRequests
	}
	if c.Region == "" {
		c.Region = DefaultRegion
	}
	for _, v := range []struct {
		name string
		d    time.Duration
	}{
		{"ReadHeaderTimeout", c.ReadHeaderTimeout},
		{"ReadBodyIdleTimeout", c.ReadBodyIdleTimeout},
		{"WriteTimeout", c.WriteTimeout},
		{"IdleTimeout", c.IdleTimeout},
		{"ShutdownTimeout", c.ShutdownTimeout},
	} {
		if v.d < 0 {
			return c, fmt.Errorf("s3api: %s must not be negative", v.name)
		}
	}
	if c.MaxConcurrentRequests < 1 || c.MaxQueuedRequests < 0 {
		return c, errors.New("s3api: MaxConcurrentRequests must be positive and MaxQueuedRequests non-negative")
	}
	return c, nil
}

func setDuration(d *time.Duration, def time.Duration) {
	if *d == 0 {
		*d = def
	}
}

// loadTLS returns the TLS configuration for the certificate pair, or nil
// when TLS is not configured.
func (c Config) loadTLS() (*tls.Config, error) {
	if c.TLSCertFile == "" {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("s3api: load TLS key pair: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
}

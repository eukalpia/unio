package s3api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
)

// Default values applied to zero Config fields.
const (
	DefaultReadHeaderTimeout   = 10 * time.Second
	DefaultReadBodyIdleTimeout = 60 * time.Second
	DefaultWriteIdleTimeout    = 60 * time.Second
	DefaultIdleTimeout         = 2 * time.Minute
	DefaultShutdownTimeout     = 30 * time.Second
	DefaultMaxConcurrentReqs   = 256
	DefaultMaxQueuedRequests   = 1024
	DefaultRegion              = "us-east-1"
	// DefaultBufferBudget bounds the memory of GET copy buffers.
	DefaultBufferBudget = 64 << 20
	// copyBufferSize is the size of one GET copy buffer.
	copyBufferSize = 64 << 10
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
	// WriteIdleTimeout bounds the wait for each write of a response, so a
	// client that stops reading is disconnected while a slow client that keeps
	// reading is not cut off.
	WriteIdleTimeout time.Duration
	// WriteTimeout caps the total time of one streamed response, for example
	// a GET of a large object. Zero means no cap.
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
	// Readiness reports whether the node can serve requests. It backs
	// /unio/health/ready. Nil means always ready.
	Readiness func(ctx context.Context) error
	// Metrics serves /unio/metrics. A request with a Bearer token must present
	// MetricsToken; any other request must be signed by a user with the admin
	// action. Nil makes the endpoint answer NotImplemented.
	Metrics http.Handler
	// MetricsToken is the bearer token that /unio/metrics accepts in an
	// "Authorization: Bearer <token>" header, for scrapers that cannot sign
	// SigV4. Nil or empty disables the token. New keeps only a SHA-256 digest
	// of it, so later changes to the slice have no effect. The token is
	// compared in constant time. Its length is checked by internal/config,
	// which requires at least 32 bytes; this package does not check it.
	//
	// A request that presents a Bearer credential is judged by the token
	// alone. A wrong, empty or missing token, or any token while none is
	// configured, is 403 AccessDenied and never falls back to SigV4. Without a
	// Bearer header the endpoint keeps the SigV4 path with the admin action.
	MetricsToken []byte
	// BufferBudget bounds the memory of GET copy buffers. Nil selects a budget
	// of DefaultBufferBudget bytes owned by the server.
	BufferBudget *bufpool.Budget
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
	setDuration(&c.WriteIdleTimeout, DefaultWriteIdleTimeout)
	setDuration(&c.IdleTimeout, DefaultIdleTimeout)
	setDuration(&c.ShutdownTimeout, DefaultShutdownTimeout)
	if c.MaxConcurrentRequests == 0 {
		c.MaxConcurrentRequests = DefaultMaxConcurrentReqs
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
		{"WriteIdleTimeout", c.WriteIdleTimeout},
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
	if c.BufferBudget == nil {
		c.BufferBudget = bufpool.NewBudget(DefaultBufferBudget)
	}
	if c.BufferBudget.Total() < copyBufferSize {
		return c, errors.New("s3api: BufferBudget must hold at least one copy buffer")
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

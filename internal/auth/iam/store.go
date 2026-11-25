package iam

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/sigv4"
	"github.com/eukalpia/unio/internal/meta"
)

// Defaults for Config.
const (
	defaultCacheTTL   = 5 * time.Second
	defaultCacheSize  = 4096
	defaultMaxRetries = 8
	defaultRegion     = "us-east-1"
)

// Config configures a Store.
type Config struct {
	// KEK is the 32-byte key-encryption key, usually from DeriveKEK. Required.
	KEK []byte
	// RootAccessKey and RootSecret are the root credentials. They are
	// required, validated by auth.ValidateRootCredentials, and kept only in
	// memory. Root bypasses policies.
	RootAccessKey string
	RootSecret    string
	// Region is the region that SigV4 credential scopes must name. Default
	// us-east-1.
	Region string
	// Clock returns the current time. Default time.Now.
	Clock func() time.Time
	// CacheTTL bounds how long a cached key or user is trusted. Changes made
	// on another node take effect within this time. Default 5 seconds.
	CacheTTL time.Duration
	// CacheSize bounds each cache generation. Default 4096.
	CacheSize int
	// MaxRetries bounds compare-and-swap retries. Default 8.
	MaxRetries int
}

// Store manages IAM state on a SystemKV and authenticates and authorizes
// requests against it. It is safe for concurrent use.
type Store struct {
	kv         meta.SystemKV
	aead       cipher.AEAD
	clock      func() time.Time
	rootKey    string
	rootSecret string
	authn      *sigv4.Authenticator
	keys       *ttlCache[string, keyLookup]
	users      *ttlCache[string, userLookup]
	maxRetries int
}

var _ auth.IAM = (*Store)(nil)

// New returns a Store over kv. It fails for a missing or malformed KEK, weak
// or missing root credentials, or a nil SystemKV.
func New(kv meta.SystemKV, cfg Config) (*Store, error) {
	if kv == nil {
		return nil, errors.New("iam: nil SystemKV")
	}
	aead, err := newAEAD(cfg.KEK)
	if err != nil {
		return nil, fmt.Errorf("iam: key-encryption key: %w", err)
	}
	if err := auth.ValidateRootCredentials(cfg.RootAccessKey, cfg.RootSecret); err != nil {
		return nil, err
	}
	if cfg.Region == "" {
		cfg.Region = defaultRegion
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultCacheTTL
	}
	if cfg.CacheSize <= 0 {
		cfg.CacheSize = defaultCacheSize
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	s := &Store{
		kv:         kv,
		aead:       aead,
		clock:      cfg.Clock,
		rootKey:    cfg.RootAccessKey,
		rootSecret: cfg.RootSecret,
		keys:       newTTLCache[string, keyLookup](cfg.Clock, cfg.CacheTTL, cfg.CacheSize),
		users:      newTTLCache[string, userLookup](cfg.Clock, cfg.CacheTTL, cfg.CacheSize),
		maxRetries: cfg.MaxRetries,
	}
	s.authn = sigv4.New(s, sigv4.Options{Region: cfg.Region, Clock: cfg.Clock})
	return s, nil
}

// update performs a compare-and-swap update of key. mutate receives the
// stored value and whether it exists, and returns the value to store. If a
// concurrent writer changes the key first, the update is retried, up to
// maxRetries attempts, after which ErrTooManyRetries is returned.
func (s *Store) update(ctx context.Context, key string, mutate func(cur []byte, exists bool) ([]byte, error)) error {
	for range s.maxRetries {
		cur, rev, err := s.kv.Get(ctx, key)
		exists := err == nil
		if err != nil && !errors.Is(err, meta.ErrNotFound) {
			return err
		}
		if !exists {
			rev = 0
		}
		next, err := mutate(cur, exists)
		if err != nil {
			return err
		}
		_, err = s.kv.Put(ctx, key, next, rev)
		if err == nil {
			return nil
		}
		if !errors.Is(err, meta.ErrRevisionMismatch) {
			return err
		}
	}
	return ErrTooManyRetries
}

// deleteKey removes key with a compare-and-swap. It returns meta.ErrNotFound
// when the key is absent and ErrTooManyRetries when retries run out.
func (s *Store) deleteKey(ctx context.Context, key string) error {
	for range s.maxRetries {
		_, rev, err := s.kv.Get(ctx, key)
		if err != nil {
			return err
		}
		err = s.kv.Delete(ctx, key, rev)
		if err == nil {
			return nil
		}
		if !errors.Is(err, meta.ErrRevisionMismatch) {
			return err
		}
	}
	return ErrTooManyRetries
}

// deleteWhile removes key with a compare-and-swap, but only while owned still
// holds for the stored value. It reports whether the key was removed. A key
// that is absent, or whose value no longer belongs to the caller, is left
// alone, so a cleanup can never remove a record written by a newer
// incarnation of the same name.
func (s *Store) deleteWhile(ctx context.Context, key string, owned func(raw []byte) bool) (bool, error) {
	for range s.maxRetries {
		raw, rev, err := s.kv.Get(ctx, key)
		if errors.Is(err, meta.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !owned(raw) {
			return false, nil
		}
		err = s.kv.Delete(ctx, key, rev)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, meta.ErrNotFound) {
			return false, nil
		}
		if !errors.Is(err, meta.ErrRevisionMismatch) {
			return false, err
		}
	}
	return false, ErrTooManyRetries
}

// clearCaches drops every cached key and user. Local administrative changes
// call it so that they take effect at once. Changes made on other nodes
// take effect when their cache entries expire.
func (s *Store) clearCaches() {
	s.keys.clear()
	s.users.clear()
}

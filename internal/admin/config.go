package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
)

// Defaults for the zero Config fields.
const (
	defaultRegion        = "us-east-1"
	defaultLoginAttempts = 10
	defaultLoginWindow   = time.Minute
	defaultLoginClients  = 4096
)

// CredentialSource resolves an access key to its secret and identity. The IAM
// store implements it. Lookup fails with sigv4.ErrUnknownAccessKey for a key
// that is unknown or whose user is disabled. The secret is used to check a
// login and to sign presigned URLs, and is never returned to a client.
type CredentialSource interface {
	Lookup(ctx context.Context, accessKey string) (secret string, identity auth.Identity, err error)
}

// Deletions reports the deletion backlog of the object engine.
type Deletions interface {
	Stats() engine.Stats
}

// Config configures a Service. Every field marked required must be set.
type Config struct {
	// IAM manages users, keys and rights, and authenticates SigV4 requests.
	// Required.
	IAM auth.IAM
	// Credentials resolves access keys to secrets. Required.
	Credentials CredentialSource
	// Layer is the object layer that buckets and objects are read and written
	// through. Required.
	Layer object.Layer
	// Cluster is the node's view of the cluster map, health and drives. Required.
	Cluster cluster.Topology
	// Deletions reports the deletion backlog. Required.
	Deletions Deletions
	// Recorder holds the request statistics behind metrics/summary. Required.
	Recorder *Recorder
	// BufferInUse reports the bytes held by data buffers. Required.
	BufferInUse func() int64
	// RootAccessKey is the access key of the root account. The root user cannot
	// be deleted, disabled or given keys through this API. Required.
	RootAccessKey string
	// SessionSecret is the cluster secret. The session key is derived from it
	// with HKDF, so every node accepts the sessions of every other node. It
	// must be at least 32 bytes. Required.
	SessionSecret []byte
	// Region is the S3 region that presigned URLs are signed for. Default
	// us-east-1.
	Region string
	// Secure marks the session cookie Secure and presigned URLs https. Set it
	// when TLS is on.
	Secure bool
	// LoginAttempts is the number of login attempts a client may make in each
	// LoginWindow. Default 10.
	LoginAttempts int
	// LoginWindow is the window of LoginAttempts. Default one minute.
	LoginWindow time.Duration
	// LoginClients bounds the number of client addresses the login limiter
	// tracks. The least recently seen address is forgotten first. Default 4096.
	LoginClients int
	// Clock returns the current time. Default time.Now.
	Clock func() time.Time
	// Logger receives request failures and refusals. Nil discards.
	Logger *slog.Logger
}

// New validates cfg and returns a Service. It derives the session key from
// the cluster secret, and fails if the secret is too short or a required
// field is missing.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.IAM == nil:
		return nil, errors.New("admin: IAM is required")
	case cfg.Credentials == nil:
		return nil, errors.New("admin: credential source is required")
	case cfg.Layer == nil:
		return nil, errors.New("admin: object layer is required")
	case cfg.Cluster == nil:
		return nil, errors.New("admin: cluster topology is required")
	case cfg.Deletions == nil:
		return nil, errors.New("admin: deletion source is required")
	case cfg.Recorder == nil:
		return nil, errors.New("admin: recorder is required")
	case cfg.BufferInUse == nil:
		return nil, errors.New("admin: buffer usage reader is required")
	case !auth.ValidAccessKeyID(cfg.RootAccessKey):
		return nil, errors.New("admin: root access key is not valid")
	}
	key, err := deriveSessionKey(cfg.SessionSecret)
	if err != nil {
		return nil, fmt.Errorf("admin: session key: %w", err)
	}
	if cfg.Region == "" {
		cfg.Region = defaultRegion
	}
	if cfg.LoginAttempts <= 0 {
		cfg.LoginAttempts = defaultLoginAttempts
	}
	if cfg.LoginWindow <= 0 {
		cfg.LoginWindow = defaultLoginWindow
	}
	if cfg.LoginClients <= 0 {
		cfg.LoginClients = defaultLoginClients
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	dummy, err := newDummySecret()
	if err != nil {
		return nil, fmt.Errorf("admin: %w", err)
	}
	return &Service{
		cfg:     cfg,
		key:     key,
		limiter: newLoginLimiter(cfg.LoginAttempts, cfg.LoginWindow, cfg.LoginClients, cfg.Clock),
		dummy:   dummy,
	}, nil
}
